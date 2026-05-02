package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Lumencast/lumencast-go/protocol"
	"github.com/Lumencast/lumencast-go/server"
)

// startTestServer launches a Server bound to an ephemeral port and
// returns its base WebSocket URL plus a tear-down func.
func startTestServer(t *testing.T, auth server.Authenticator) (*server.Server, string) {
	t.Helper()
	srv, err := server.New(server.Config{
		ListenAddr:       "127.0.0.1:0",
		Auth:             auth,
		PingInterval:     5 * time.Second,
		SubscribeTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan struct{})
	go func() {
		_ = srv.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	})
	// Wait for Addr() to populate.
	deadline := time.Now().Add(2 * time.Second)
	for srv.Addr() == "" {
		if time.Now().After(deadline) {
			t.Fatal("server never reported Addr()")
		}
		time.Sleep(5 * time.Millisecond)
	}
	return srv, "ws://" + srv.Addr() + "/lsdp.v1"
}

func dial(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{
		Subprotocols: []string{protocol.SubProtocol},
		HTTPClient:   &http.Client{Timeout: 2 * time.Second},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = c.Close(websocket.StatusNormalClosure, "") })
	return c
}

func send(t *testing.T, c *websocket.Conn, msg any) {
	t.Helper()
	raw, err := protocol.Encode(msg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageText, raw); err != nil {
		t.Fatal(err)
	}
}

func recv(t *testing.T, c *websocket.Conn) any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, raw, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := protocol.DecodeServer(raw)
	if err != nil {
		t.Fatalf("decode %q: %v", raw, err)
	}
	return msg
}

func opAuth() server.Authenticator {
	return server.NewStaticTokens(map[string]server.Identity{
		"op-tok":     {Subject: "op", Role: protocol.RoleOperator},
		"viewer-tok": {Subject: "v", Role: protocol.RoleViewer},
		"svc-tok":    {Subject: "svc", Role: protocol.RoleService, Paths: []string{"__inputs.score.*"}},
	})
}

func TestSubscribe_LiveFlow(t *testing.T) {
	srv, url := startTestServer(t, opAuth())
	scene := srv.NewScene("main")
	if err := scene.Set(map[string]any{"show.title": "Hello"}); err != nil {
		t.Fatal(err)
	}

	c := dial(t, url)
	send(t, c, &protocol.Subscribe{Token: "viewer-tok"})

	snap, ok := recv(t, c).(*protocol.Snapshot)
	if !ok {
		t.Fatalf("first frame is not snapshot")
	}
	if snap.Seq != 1 || snap.SceneID != "main" {
		t.Fatalf("unexpected snapshot: %+v", snap)
	}
	var title string
	_ = json.Unmarshal(snap.State["show.title"], &title)
	if title != "Hello" {
		t.Fatalf("snapshot state missing title")
	}
}

func TestSubscribe_RejectsUnknownToken(t *testing.T) {
	_, url := startTestServer(t, opAuth())

	c := dial(t, url)
	send(t, c, &protocol.Subscribe{Token: "nope"})
	frame := recv(t, c)
	errFrame, ok := frame.(*protocol.Error)
	if !ok {
		t.Fatalf("got %T, want *Error", frame)
	}
	if errFrame.Code != string(protocol.CodeAuthDenied) {
		t.Fatalf("got code %q", errFrame.Code)
	}
}

func TestInput_OperatorWritesInputs(t *testing.T) {
	srv, url := startTestServer(t, opAuth())
	scene := srv.NewScene("main", server.WithDeclaredInputs([]string{"__inputs.title"}))
	_ = scene.Set(map[string]any{"x": 1})

	c := dial(t, url)
	send(t, c, &protocol.Subscribe{Token: "op-tok"})
	_ = recv(t, c) // discard snapshot

	send(t, c, &protocol.Input{
		Patches: []protocol.Patch{
			{Path: "__inputs.title", Value: json.RawMessage(`"hi"`)},
		},
	})
	d, ok := recv(t, c).(*protocol.Delta)
	if !ok {
		t.Fatalf("expected delta echo")
	}
	if len(d.Patches) != 1 || d.Patches[0].Path != "__inputs.title" {
		t.Fatalf("unexpected delta: %+v", d)
	}
}

func TestInput_ViewerForbidden(t *testing.T) {
	srv, url := startTestServer(t, opAuth())
	scene := srv.NewScene("main")
	_ = scene.Set(map[string]any{"x": 1})

	c := dial(t, url)
	send(t, c, &protocol.Subscribe{Token: "viewer-tok"})
	_ = recv(t, c) // snapshot

	send(t, c, &protocol.Input{
		Patches: []protocol.Patch{{Path: "__inputs.title", Value: json.RawMessage(`"x"`)}},
	})
	frame := recv(t, c)
	e, ok := frame.(*protocol.Error)
	if !ok {
		t.Fatalf("got %T, want *Error", frame)
	}
	if e.Code != string(protocol.CodeWriteForbidden) {
		t.Fatalf("code %q", e.Code)
	}
}

func TestInput_UnknownPathRejected(t *testing.T) {
	srv, url := startTestServer(t, opAuth())
	scene := srv.NewScene("main", server.WithDeclaredInputs([]string{"__inputs.title"}))
	_ = scene.Set(map[string]any{"x": 1})

	c := dial(t, url)
	send(t, c, &protocol.Subscribe{Token: "op-tok"})
	_ = recv(t, c)

	send(t, c, &protocol.Input{
		Patches: []protocol.Patch{{Path: "__inputs.unknown", Value: json.RawMessage(`1`)}},
	})
	e := recv(t, c).(*protocol.Error)
	if e.Code != string(protocol.CodeUnknownPath) {
		t.Fatalf("code %q", e.Code)
	}
}

func TestPing_GetsPong(t *testing.T) {
	srv, url := startTestServer(t, opAuth())
	srv.NewScene("main")
	_ = srv.ActiveScene().Set(map[string]any{"a": 1})

	c := dial(t, url)
	send(t, c, &protocol.Subscribe{Token: "viewer-tok"})
	_ = recv(t, c)

	send(t, c, &protocol.Ping{})
	frame := recv(t, c)
	if _, ok := frame.(*protocol.Pong); !ok {
		t.Fatalf("got %T, want *Pong", frame)
	}
}

func TestSubscribe_RequiresLSDPSubprotocol(t *testing.T) {
	srv, _ := startTestServer(t, opAuth())
	srv.NewScene("main")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	wsURL := "ws://" + srv.Addr() + "/lsdp.v1"
	c, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		Subprotocols: []string{"not-lsdp"},
	})
	if err != nil {
		// Server-side outright rejection is also acceptable.
		return
	}
	defer c.Close(websocket.StatusNormalClosure, "")
	// Server selected no common subprotocol → empty string. coder/websocket
	// reports "" rather than an explicit failure.
	if got := c.Subprotocol(); got == protocol.SubProtocol {
		t.Fatalf("server accepted non-LSDP subprotocol: %q", got)
	}
	// Server should close the connection promptly.
	readCtx, cancelRead := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancelRead()
	if _, _, err := c.Read(readCtx); err == nil {
		t.Fatalf("expected read failure after wrong subprotocol")
	}
}

func TestStaticTokens_Authenticator(t *testing.T) {
	tok := server.NewStaticTokens(map[string]server.Identity{
		"a": {Subject: "alice", Role: protocol.RoleOperator},
	})
	id, err := tok.Authenticate(context.Background(), "a")
	if err != nil || id.Role != protocol.RoleOperator {
		t.Fatalf("unexpected: %+v %v", id, err)
	}
	if _, err := tok.Authenticate(context.Background(), "missing"); err == nil {
		t.Fatal("missing token should fail")
	}
}

func TestIdentity_CanWrite(t *testing.T) {
	op := server.Identity{Role: protocol.RoleOperator}
	if !op.CanWrite("__inputs.x") {
		t.Fail()
	}
	if op.CanWrite("__test.x") {
		t.Fail()
	}

	svc := server.Identity{Role: protocol.RoleService, Paths: []string{"__inputs.score.*"}}
	if !svc.CanWrite("__inputs.score.alice") {
		t.Fail()
	}
	if svc.CanWrite("__inputs.title") {
		t.Fail()
	}

	tst := server.Identity{Role: protocol.RoleTest}
	if !tst.CanWrite("__test.foo") {
		t.Fail()
	}
	if tst.CanWrite("__inputs.x") {
		t.Fail()
	}

	v := server.Identity{Role: protocol.RoleViewer}
	if v.CanWrite("__inputs.x") {
		t.Fail()
	}
}

func TestStoreEncodesValues(t *testing.T) {
	srv, _ := startTestServer(t, opAuth())
	scene := srv.NewScene("s")
	if err := scene.Set(map[string]any{"a": 1, "b": "two", "c": []int{1, 2}}); err != nil {
		t.Fatal(err)
	}
	// Ensure invalid raw rejected.
	if err := scene.Set(map[string]any{"x": json.RawMessage("not json")}); err == nil {
		t.Fatal("invalid raw must error")
	}
}

func TestSceneRefreshOnSet(t *testing.T) {
	srv, url := startTestServer(t, opAuth())
	scene := srv.NewScene("main")
	_ = scene.Set(map[string]any{"v": 1})

	c := dial(t, url)
	send(t, c, &protocol.Subscribe{Token: "viewer-tok"})
	_ = recv(t, c) // initial snapshot

	// Subsequent Set fans out a fresh snapshot.
	if err := scene.Set(map[string]any{"v": 2}); err != nil {
		t.Fatal(err)
	}
	frame := recv(t, c)
	snap, ok := frame.(*protocol.Snapshot)
	if !ok {
		t.Fatalf("got %T, want *Snapshot after Set", frame)
	}
	if snap.Seq != 1 {
		t.Fatalf("snapshot seq must reset to 1, got %d", snap.Seq)
	}
}

func TestSetActive_EmitsSceneChangedToLiveSubs(t *testing.T) {
	srv, url := startTestServer(t, opAuth())

	a := srv.NewScene("a", server.WithSceneVersion("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))
	_ = a.Set(map[string]any{"v": "from-a"})
	b := srv.NewScene("b", server.WithSceneVersion("sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"))
	_ = b.Set(map[string]any{"v": "from-b"})

	c := dial(t, url)
	send(t, c, &protocol.Subscribe{Token: "viewer-tok"}) // live mode (no scene field)
	snap := recv(t, c).(*protocol.Snapshot)
	if snap.SceneID != "a" {
		t.Fatalf("first snapshot should be from a, got %s", snap.SceneID)
	}

	if err := srv.SetActive("b"); err != nil {
		t.Fatal(err)
	}

	// Live subscriber should observe SceneChanged then Snapshot at seq=1.
	change := recv(t, c)
	sc, ok := change.(*protocol.SceneChanged)
	if !ok {
		t.Fatalf("got %T, want *SceneChanged", change)
	}
	if sc.SceneID != "b" {
		t.Fatalf("scene_changed.scene_id = %s, want b", sc.SceneID)
	}
	next := recv(t, c)
	snap2, ok := next.(*protocol.Snapshot)
	if !ok {
		t.Fatalf("after scene_changed got %T, want *Snapshot", next)
	}
	if snap2.Seq != 1 {
		t.Fatalf("snapshot seq after scene_changed = %d, want 1", snap2.Seq)
	}
	if snap2.SceneID != "b" {
		t.Fatalf("snapshot scene_id = %s, want b", snap2.SceneID)
	}
	var v string
	_ = json.Unmarshal(snap2.State["v"], &v)
	if v != "from-b" {
		t.Fatalf("snapshot state v = %q, want from-b", v)
	}
}

func TestSetActive_NoOpForSameScene(t *testing.T) {
	srv, url := startTestServer(t, opAuth())
	srv.NewScene("a")
	_ = srv.ActiveScene().Set(map[string]any{"v": 1})

	c := dial(t, url)
	send(t, c, &protocol.Subscribe{Token: "viewer-tok"})
	_ = recv(t, c) // initial snapshot

	if err := srv.SetActive("a"); err != nil {
		t.Fatal(err)
	}

	// No frame should arrive in the next ~150 ms.
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_, _, err := c.Read(ctx)
	if err == nil {
		t.Fatal("unexpected frame after no-op SetActive")
	}
}

func TestEmitFanOut(t *testing.T) {
	srv, url := startTestServer(t, opAuth())
	scene := srv.NewScene("main")
	_ = scene.Set(map[string]any{"v": 0})

	c := dial(t, url)
	send(t, c, &protocol.Subscribe{Token: "viewer-tok"})
	_ = recv(t, c) // snap
	if err := scene.Emit(map[string]any{"v": 5}); err != nil {
		t.Fatal(err)
	}
	d := recv(t, c).(*protocol.Delta)
	if d.Seq != 2 {
		t.Fatalf("delta seq = %d, want 2", d.Seq)
	}
	if !strings.Contains(string(d.Patches[0].Value), "5") {
		t.Fatalf("patch missing value 5")
	}
}
