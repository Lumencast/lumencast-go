package control_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Lumencast/lumencast-go/interop/control"
	"github.com/Lumencast/lumencast-go/protocol"
	"github.com/Lumencast/lumencast-go/server"
)

// newPlaneHTTP wires a control plane onto two test servers — one
// emulating the LSDP/1 endpoint, one mounting /test/*. Returns the
// control test server (closed by the caller).
func newPlaneHTTP(t *testing.T) (*httptest.Server, *server.Server, *server.StaticTokens) {
	t.Helper()
	auth := server.NewStaticTokens(map[string]server.Identity{})
	srv, err := server.New(server.Config{
		ListenAddr: "127.0.0.1:0",
		Auth:       auth,
	})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	plane := control.New(srv, auth, "ws://test/lsdp.v1")
	ts := httptest.NewServer(plane.Mux())
	t.Cleanup(ts.Close)
	return ts, srv, auth
}

func TestSetupResetState(t *testing.T) {
	ts, _, auth := newPlaneHTTP(t)

	// Setup with one bundle.
	setup := map[string]any{
		"scenario": "subscribe-snapshot-delta",
		"tokens": map[string]string{
			"$TOKEN_OPERATOR": "tok-op",
			"$TOKEN_VIEWER":   "tok-vw",
			"$TOKEN_INVALID":  "tok-inv-ignored",
		},
		"bundles": []map[string]any{
			{
				"id":   "t",
				"hash": "sha256:f1d2d2",
				"inline": map[string]any{
					"v":     1,
					"kind":  "frame",
					"id":    "t",
					"state": map[string]any{"title": "Hello"},
				},
			},
		},
		"initial_state": map[string]any{"title": "Hello", "count": 0},
	}
	body, _ := json.Marshal(setup)
	resp := postJSON(t, ts.URL+"/test/setup", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("setup: status %d", resp.StatusCode)
	}
	var sresp struct {
		WSUrl        string `json:"ws_url"`
		SceneID      string `json:"scene_id"`
		SceneVersion string `json:"scene_version"`
	}
	dec(t, resp, &sresp)
	if sresp.SceneID != "t" || sresp.SceneVersion != "sha256:f1d2d2" {
		t.Fatalf("setup response wrong: %+v", sresp)
	}

	// State endpoint should reflect the seeded state.
	stateResp := getJSON(t, ts.URL+"/test/state")
	if stateResp.StatusCode != http.StatusOK {
		t.Fatalf("state: status %d", stateResp.StatusCode)
	}
	var st struct {
		SceneID string         `json:"scene_id"`
		State   map[string]any `json:"state"`
	}
	dec(t, stateResp, &st)
	if st.SceneID != "t" || st.State["title"] != "Hello" {
		t.Fatalf("unexpected state: %+v", st)
	}

	// $TOKEN_INVALID must not be installed in the auth store.
	if _, err := auth.Authenticate(t.Context(), "tok-inv-ignored"); err == nil {
		t.Fatalf("$TOKEN_INVALID should not be authenticatable")
	}
	// Operator and viewer tokens MUST authenticate.
	if _, err := auth.Authenticate(t.Context(), "tok-op"); err != nil {
		t.Fatalf("operator token should authenticate: %v", err)
	}
	if _, err := auth.Authenticate(t.Context(), "tok-vw"); err != nil {
		t.Fatalf("viewer token should authenticate: %v", err)
	}

	// Reset clears state.
	resp = postJSON(t, ts.URL+"/test/reset", nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("reset: status %d", resp.StatusCode)
	}
	stateResp = getJSON(t, ts.URL+"/test/state")
	if stateResp.StatusCode != http.StatusConflict {
		t.Fatalf("post-reset state: expected 409, got %d", stateResp.StatusCode)
	}
}

func TestEmitWithoutSetupReturnsConflict(t *testing.T) {
	ts, _, _ := newPlaneHTTP(t)
	body, _ := json.Marshal(map[string]any{
		"patches": []map[string]any{{"path": "x", "value": 1}},
	})
	resp := postJSON(t, ts.URL+"/test/emit", body)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("emit without setup: expected 409, got %d", resp.StatusCode)
	}
}

// /test/setup registers a scene whose inline bundle carries a
// malformed `x-zab.capture`, but marks it unservable so subscribers get
// INVALID_VALUE instead of a snapshot (RFC-0001 + Amendment 2). Setup
// itself still answers 200 : the scenario drives the rejection over the
// WebSocket, not over the control plane.
func TestSetupRejectsMalformedZabCapture(t *testing.T) {
	cases := map[string]map[string]any{
		"bare device id": {
			"kind": "x-zab.capture", "id": "cam",
			"x-zab.sourceKind": "media.webcam",
			"x-zab.deviceRef":  "video:0",
			"size":             map[string]any{"w": 640, "h": 360},
		},
		"media.file without size": {
			"kind": "x-zab.capture", "id": "intro",
			"x-zab.sourceKind": "media.file",
			"x-zab.deviceRef":  "intro-sting",
		},
	}
	for name, node := range cases {
		t.Run(name, func(t *testing.T) {
			ts, srv, _ := newPlaneHTTP(t)
			body, _ := json.Marshal(map[string]any{
				"bundles": []map[string]any{{
					"id":   "capture",
					"hash": "sha256:cabdead",
					"inline": map[string]any{
						"scene_id": "capture",
						"layout": map[string]any{
							"kind":     "frame",
							"children": []any{node},
						},
					},
				}},
			})
			if resp := postJSON(t, ts.URL+"/test/setup", body); resp.StatusCode != http.StatusOK {
				t.Fatalf("setup: status %d", resp.StatusCode)
			}
			scene, ok := srv.Scene("capture")
			if !ok {
				t.Fatal("scene not registered")
			}
			rej := scene.Rejection()
			if rej == nil {
				t.Fatal("malformed x-zab.capture must mark the scene unservable")
			}
			if rej.Code != protocol.CodeInvalidValue {
				t.Fatalf("got code %q, want INVALID_VALUE", rej.Code)
			}
		})
	}
}

// A well-formed capture bundle stays servable.
func TestSetupAcceptsValidZabCapture(t *testing.T) {
	ts, srv, _ := newPlaneHTTP(t)
	body, _ := json.Marshal(map[string]any{
		"bundles": []map[string]any{{
			"id":   "capture",
			"hash": "sha256:cabdead",
			"inline": map[string]any{
				"scene_id": "capture",
				"layout": map[string]any{
					"kind": "frame",
					"children": []any{map[string]any{
						"kind": "x-zab.capture", "id": "mic",
						"x-zab.sourceKind": "media.mic",
						"x-zab.deviceRef":  "main-mic",
					}},
				},
			},
		}},
	})
	if resp := postJSON(t, ts.URL+"/test/setup", body); resp.StatusCode != http.StatusOK {
		t.Fatalf("setup: status %d", resp.StatusCode)
	}
	scene, ok := srv.Scene("capture")
	if !ok {
		t.Fatal("scene not registered")
	}
	if rej := scene.Rejection(); rej != nil {
		t.Fatalf("valid capture bundle must stay servable, got %+v", rej)
	}
}

func TestHealth(t *testing.T) {
	ts, _, _ := newPlaneHTTP(t)
	resp := getJSON(t, ts.URL+"/test/health")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health: status %d", resp.StatusCode)
	}
	var h map[string]any
	dec(t, resp, &h)
	if h["control_plane_version"].(float64) != 1 {
		t.Fatalf("health: unexpected version %v", h["control_plane_version"])
	}
}

// helpers ----------------------------------------------------------

func postJSON(t *testing.T, url string, body []byte) *http.Response {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, _ := http.NewRequest(http.MethodPost, url, rdr)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post %s: %v", url, err)
	}
	return resp
}

func getJSON(t *testing.T, url string) *http.Response {
	t.Helper()
	resp, err := http.Get(url) //nolint:gosec,noctx
	if err != nil {
		t.Fatalf("get %s: %v", url, err)
	}
	return resp
}

func dec(t *testing.T, resp *http.Response, out any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

