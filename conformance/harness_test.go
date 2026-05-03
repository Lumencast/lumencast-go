//go:build conformance

package conformance_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Lumencast/lumencast-go/conformance"
	"github.com/Lumencast/lumencast-go/protocol"
	"github.com/Lumencast/lumencast-go/server"
)

// inProcessDriver bridges the harness to a freshly-reset in-process
// server.Server for every scenario. It implements conformance.Driver
// (and BundleAwareDriver).
type inProcessDriver struct {
	mu  sync.Mutex
	srv *server.Server

	url string
}

func newInProcessDriver(t *testing.T) *inProcessDriver {
	t.Helper()
	d := &inProcessDriver{}
	srv, err := server.New(server.Config{
		ListenAddr: "127.0.0.1:0",
		Auth: server.NewStaticTokens(map[string]server.Identity{
			"op-tok":     {Subject: "op", Role: protocol.RoleOperator},
			"viewer-tok": {Subject: "v", Role: protocol.RoleViewer},
			"svc-tok":    {Subject: "svc", Role: protocol.RoleService, Paths: []string{"__inputs.*"}},
			"test-tok":   {Subject: "tst", Role: protocol.RoleTest},
		}),
		PingInterval:     30 * time.Second,
		SubscribeTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	d.srv = srv

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = srv.Run(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	for srv.Addr() == "" {
		if time.Now().After(deadline) {
			t.Fatal("server never bound")
		}
		time.Sleep(5 * time.Millisecond)
	}
	d.url = "ws://" + srv.Addr() + "/lsdp.v1"
	return d
}

func (d *inProcessDriver) Setup(scenarioName string) (string, map[string]string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Recreate the live "t" scene with the canonical scene version
	// fixture and an empty initial state. Per-scenario tweaks are
	// keyed off the scenario name.
	switch scenarioName {
	case "subscribe-snapshot-delta":
		// This scenario doesn't declare a bundle ; it expects the
		// scene to expose user-facing leaves directly (no
		// __inputs.* prefix). Seed accordingly.
		scene := d.srv.NewScene("t",
			server.WithSceneVersion("sha256:f1d2d2f924e986ac86fdf7b36c94bcdf32beec15c3aef0d27b6bc8f8a90b9e3f"),
		)
		_ = scene.Set(map[string]any{
			"title": "Hello",
			"count": 0,
		})
	default:
		scene := d.srv.NewScene("t",
			server.WithSceneVersion("sha256:f1d2d2f924e986ac86fdf7b36c94bcdf32beec15c3aef0d27b6bc8f8a90b9e3f"),
			server.WithDeclaredInputs([]string{"__inputs.title"}),
		)
		// Per-scenario initial state. Most scenarios start with an
		// empty title ; a few rely on a populated initial value.
		initialTitle := ""
		if scenarioName == "unknown-path-rejected" {
			initialTitle = "Hello"
		}
		_ = scene.Set(map[string]any{"__inputs.title": initialTitle})
	}

	// test-session-namespace expects a separate "test-scene".
	if scenarioName == "test-session-namespace" {
		_ = d.srv.NewScene("test-scene",
			server.WithSceneVersion("sha256:f1d2d2f924e986ac86fdf7b36c94bcdf32beec15c3aef0d27b6bc8f8a90b9e3f"),
		)
	}

	tokens := map[string]string{
		"$TOKEN_OPERATOR": "op-tok",
		"$TOKEN_VIEWER":   "viewer-tok",
		"$TOKEN_SERVICE":  "svc-tok",
		"$TOKEN_TEST":     "test-tok",
		"$TOKEN_INVALID":  "definitely-not-a-token",
	}
	return d.url, tokens, nil
}

// OnBundles is invoked by the harness after Setup, when the scenario
// declares inline bundles. We rewrite the active scene to match the
// canonical hash and apply the bundle's operator_inputs constraints +
// defaults so input frames are validated as the bundle expects.
func (d *inProcessDriver) OnBundles(_ string, bundles []conformance.ResolvedBundle) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(bundles) == 0 {
		return nil
	}
	b := bundles[0]
	sceneID, _ := b.Inline["scene_id"].(string)
	if sceneID == "" {
		sceneID = "t"
	}
	specs := extractInputSpecs(b.Inline)
	scene := d.srv.NewScene(sceneID,
		server.WithSceneVersion(b.Hash),
		server.WithOperatorInputs(specs),
	)
	if defaults, ok := b.Inline["defaults"].(map[string]any); ok && len(defaults) > 0 {
		_ = scene.Set(defaults)
	} else {
		// Conformance scenarios sometimes assert on a leaf even when
		// defaults are empty ; seed an empty placeholder so the
		// snapshot has the path.
		_ = scene.Set(map[string]any{"__inputs.title": ""})
	}
	return nil
}

// extractInputSpecs maps the bundle's operator_inputs section into
// InputSpec values the server enforces. Only the constraints that the
// scenarios use today are wired ; extend as needed.
func extractInputSpecs(inline map[string]any) []server.InputSpec {
	raw, ok := inline["operator_inputs"].([]any)
	if !ok {
		return nil
	}
	out := make([]server.InputSpec, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		spec := server.InputSpec{}
		spec.Path, _ = m["path"].(string)
		spec.Type, _ = m["type"].(string)
		if c, ok := m["constraints"].(map[string]any); ok {
			if v, ok := c["maxLength"].(int); ok {
				spec.MaxLength = v
			} else if v, ok := c["maxLength"].(float64); ok {
				spec.MaxLength = int(v)
			}
		}
		out = append(out, spec)
	}
	return out
}

func (d *inProcessDriver) SnapshotState() map[string]any {
	d.mu.Lock()
	defer d.mu.Unlock()
	scene := d.srv.ActiveScene()
	if scene == nil {
		return map[string]any{}
	}
	state := scene.SnapshotForConformance()
	out := make(map[string]any, len(state))
	for k, raw := range state {
		var v any
		_ = json.Unmarshal(raw, &v)
		out[k] = v
	}
	return out
}

// Emit implements EmittingDriver. server-emits scenario steps invoke
// this to drive the active scene through Scene.Emit, producing a
// real delta on every subscriber.
func (d *inProcessDriver) Emit(patches []map[string]any) error {
	d.mu.Lock()
	scene := d.srv.ActiveScene()
	d.mu.Unlock()
	if scene == nil {
		return fmt.Errorf("inProcessDriver: no active scene")
	}
	flat := make(map[string]any, len(patches))
	for _, p := range patches {
		path, _ := p["path"].(string)
		flat[path] = p["value"]
	}
	return scene.Emit(flat)
}

func TestConformanceSuite_InProcess(t *testing.T) {
	driver := newInProcessDriver(t)
	cfg := conformance.Config{
		Driver:    driver,
		TagFilter: conformance.TagRequired,
		// token-rotation-no-flicker has target:runtime so it's
		// auto-skipped by the harness ; we don't need to list it
		// here. subscribe-snapshot-delta now uses `server-emits` to
		// orchestrate its mid-flight deltas — supported as long as
		// the driver implements EmittingDriver.
	}
	conformance.Run(t, cfg)
}
