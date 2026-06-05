package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/Lumencast/lumencast-go/protocol"
)

// Config configures a Server. The zero value is not usable ; ListenAddr
// and Auth are required.
type Config struct {
	// ListenAddr is the TCP address Run() binds to. Use ":0" in tests
	// to get an OS-assigned port (then read it via Server.Addr()).
	ListenAddr string

	// Auth is the credential validator called on every WebSocket
	// subscribe frame. The kit ships StaticTokens for development.
	//
	// REQUIRED unless IdentityFromRequest is set : New rejects a Config
	// where both Auth and IdentityFromRequest are nil.
	Auth Authenticator

	// IdentityFromRequest is the optional header-trust seam (ADR 007
	// §C.3a). When non-nil, the server derives the connection's Identity
	// from the upgrade *http.Request — typically a header injected by a
	// trusted front-proxy — and IGNORES the Subscribe frame's token.
	// When nil, the server falls back to the Auth token path.
	//
	// Additive and non-breaking : leaving this nil preserves the exact
	// token-authentication behaviour. If both Auth and IdentityFromRequest
	// are set, IdentityFromRequest takes precedence.
	IdentityFromRequest RequestIdentityFunc

	// Logger receives structured server events. Defaults to
	// slog.Default() if nil.
	Logger *slog.Logger

	// PingInterval is the server-initiated ping cadence. Defaults to
	// 30 s, matching the LSDP/1 § 12 SHOULD.
	PingInterval time.Duration

	// SubscribeTimeout is how long the server waits for the first
	// Subscribe frame before closing the WS. Defaults to 30 s.
	SubscribeTimeout time.Duration

	// MaxFrameBytes is the largest text frame the server accepts.
	// Defaults to 64 KiB per LSDP/1 § 14.3.
	MaxFrameBytes int64

	// HTTPHandler, if non-nil, is mounted at root for non-LSDP
	// traffic (static assets, /healthz, ...). LSDP routes are mounted
	// in front of it. Use Server.Mux() to compose.
	HTTPHandler http.Handler
}

// Server is the LSDP/1 server kit. It owns a set of Scenes and a
// WebSocket handler. Construct with New and run with Run.
type Server struct {
	cfg    Config
	logger *slog.Logger

	mu     sync.RWMutex
	scenes map[string]*Scene
	active string // id of the currently-active scene for the live endpoint

	httpSrv *http.Server
	addr    string
}

// New constructs a Server. Returns an error if Config is incomplete.
func New(cfg Config) (*Server, error) {
	if cfg.ListenAddr == "" {
		return nil, errors.New("server: Config.ListenAddr is required")
	}
	if cfg.Auth == nil && cfg.IdentityFromRequest == nil {
		return nil, errors.New("server: Config.Auth or Config.IdentityFromRequest is required")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.PingInterval == 0 {
		cfg.PingInterval = 30 * time.Second
	}
	if cfg.SubscribeTimeout == 0 {
		cfg.SubscribeTimeout = 30 * time.Second
	}
	if cfg.MaxFrameBytes == 0 {
		cfg.MaxFrameBytes = 64 * 1024
	}
	return &Server{
		cfg:    cfg,
		logger: cfg.Logger,
		scenes: make(map[string]*Scene),
	}, nil
}

// NewScene registers a new Scene under the given id and returns a
// handle. Reusing an id replaces the previous Scene atomically and
// triggers a SceneChanged on the live endpoint.
func (s *Server) NewScene(id string, opts ...SceneOption) *Scene {
	scene := newScene(id, opts...)
	s.mu.Lock()
	prev := s.scenes[id]
	s.scenes[id] = scene
	if s.active == "" {
		s.active = id
	}
	s.mu.Unlock()
	if prev != nil {
		// Best-effort : detach old subscribers. They'll reconnect
		// against the new scene through the live endpoint.
		prev.mu.Lock()
		for sub := range prev.subscribers {
			sub.close()
		}
		prev.subscribers = nil
		prev.mu.Unlock()
	}
	return scene
}

// Scene looks up a registered scene by id.
func (s *Server) Scene(id string) (*Scene, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sc, ok := s.scenes[id]
	return sc, ok
}

// SetActive changes which scene the live endpoint serves. Live
// subscribers attached to the previous active scene receive a
// SceneChanged frame followed by a fresh Snapshot at seq=1 on the new
// scene. Idempotent : setting the already-active scene is a no-op.
// Returns ErrSceneNotFound if id is unknown.
func (s *Server) SetActive(id string) error {
	s.mu.Lock()
	scene, ok := s.scenes[id]
	if !ok {
		s.mu.Unlock()
		return ErrSceneNotFound
	}
	if s.active == id {
		s.mu.Unlock()
		return nil
	}
	prev := s.scenes[s.active]
	s.active = id
	s.mu.Unlock()

	if prev != nil && prev != scene {
		migrateLive(prev, scene)
	}
	return nil
}

// detach removes a subscription from whichever scene currently owns
// it. Used as the deferred cleanup in the WS handler so a migration
// during SetActive does not leak.
func (s *Server) detach(sub *subscription) {
	s.mu.RLock()
	scenes := make([]*Scene, 0, len(s.scenes))
	for _, sc := range s.scenes {
		scenes = append(scenes, sc)
	}
	s.mu.RUnlock()
	for _, sc := range scenes {
		sc.unsubscribe(sub)
	}
}

// migrateLive moves every live subscription from prev to next. It
// emits SceneChanged on prev (with prev's seq advancing one final
// step), then a fresh Snapshot at next's current seq (typically 1
// for a freshly-created scene). Per LSDP/1.1 §18.1.1, the scene's
// seq counter is independent across scenes.
func migrateLive(prev, next *Scene) {
	prev.mu.Lock()
	live := make([]*subscription, 0, len(prev.subscribers))
	for sub := range prev.subscribers {
		if sub.live {
			live = append(live, sub)
		}
	}
	for _, sub := range live {
		delete(prev.subscribers, sub)
	}
	// Advance prev's seq one last time for the SceneChanged frame —
	// SceneChanged is the prev scene's final wire emission.
	prevSeq := prev.seq.NextServer()
	prev.mu.Unlock()

	if len(live) == 0 {
		return
	}

	next.mu.Lock()
	state := next.store.snapshot()
	nextSeq := next.seq.Current()
	for _, sub := range live {
		sc := &protocol.SceneChanged{
			Seq:          prevSeq,
			SceneID:      next.id,
			SceneVersion: next.version,
		}
		select {
		case sub.out <- sc:
		default:
			sub.markStale()
			continue
		}
		snap := &protocol.Snapshot{
			Seq:          nextSeq,
			SceneID:      next.id,
			SceneVersion: next.version,
			State:        state,
		}
		select {
		case sub.out <- snap:
		default:
			sub.markStale()
			continue
		}
		next.subscribers[sub] = struct{}{}
	}
	next.mu.Unlock()
}

// ActiveScene returns the live scene, or nil if none is registered.
func (s *Server) ActiveScene() *Scene {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.active == "" {
		return nil
	}
	return s.scenes[s.active]
}

// ErrSceneNotFound is returned by SetActive and Scene when the id is
// unknown.
var ErrSceneNotFound = errors.New("server: scene not found")

// Reset drops every registered scene and clears active state. In-flight
// subscribers are detached. Intended for test harnesses (interop
// control plane) — not for production use.
func (s *Server) Reset() {
	s.mu.Lock()
	scenes := s.scenes
	s.scenes = make(map[string]*Scene)
	s.active = ""
	s.mu.Unlock()
	for _, sc := range scenes {
		sc.mu.Lock()
		for sub := range sc.subscribers {
			sub.close()
		}
		sc.subscribers = nil
		sc.mu.Unlock()
	}
}

// Mux returns a configured http.ServeMux exposing :
//
//   - /lsdp.v1                 — LSDP/1 WebSocket subscribe endpoint
//   - /scenes/{id}             — per-scene scope (test mode + static)
//   - /healthz                 — simple liveness probe
//   - the user-supplied Config.HTTPHandler at root (lowest priority)
//
// The mux is regenerated on each call ; do not modify the returned
// instance after Server.Run is called.
func (s *Server) Mux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/lsdp.v1", s.serveLSDP)
	mux.HandleFunc("/healthz", s.serveHealth)
	if s.cfg.HTTPHandler != nil {
		mux.Handle("/", s.cfg.HTTPHandler)
	}
	return mux
}

// Addr returns the bound address after Run() has started, or empty
// before that. Useful in tests with ListenAddr ":0".
func (s *Server) Addr() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.addr
}

// Run binds and serves until ctx is cancelled. The returned error is
// non-nil only on listener / Serve failure ; clean shutdown via
// ctx.Done returns nil.
func (s *Server) Run(ctx context.Context) error {
	mux := s.Mux()
	srv := &http.Server{
		Addr:              s.cfg.ListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	listener, err := netListen("tcp", s.cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("server: listen: %w", err)
	}

	s.mu.Lock()
	s.httpSrv = srv
	s.addr = listener.Addr().String()
	s.mu.Unlock()

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(listener)
	}()

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func (s *Server) serveHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}
