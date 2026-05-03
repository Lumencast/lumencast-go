package harness_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/Lumencast/lumencast-go/interop/control"
	"github.com/Lumencast/lumencast-go/interop/harness"
	"github.com/Lumencast/lumencast-go/server"
)

// TestHTTPDriver_HealthCheck spins up a real control plane in front of
// a fresh server and verifies the harness can reach it.
func TestHTTPDriver_HealthCheck(t *testing.T) {
	ts := newControlPlane(t)
	drv, err := harness.NewHTTPDriver(ts.URL, canonicalTokens())
	if err != nil {
		t.Fatalf("NewHTTPDriver: %v", err)
	}
	if err := drv.HealthCheck(context.Background()); err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
}

// TestHTTPDriver_SetupSnapshotCycle exercises Setup → SnapshotState →
// Reset against a real control plane. We pick a scenario that has
// declared bundles to validate the inline-LSML extraction path.
func TestHTTPDriver_SetupSnapshotCycle(t *testing.T) {
	ts := newControlPlane(t)
	drv, err := harness.NewHTTPDriver(ts.URL, canonicalTokens())
	if err != nil {
		t.Fatalf("NewHTTPDriver: %v", err)
	}
	url, tokens, err := drv.Setup("invalid-value-rejected")
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if url == "" {
		t.Fatal("Setup returned empty WS URL")
	}
	if got := tokens["$TOKEN_OPERATOR"]; got == "" {
		t.Fatalf("Setup returned no operator token: %+v", tokens)
	}
	state := drv.SnapshotState()
	if state == nil {
		t.Fatal("SnapshotState returned nil after Setup")
	}
	if v := state["__inputs.title"]; v != "" {
		t.Fatalf("expected seeded __inputs.title='', got %v", v)
	}
	if err := drv.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}
}

// TestHTTPDriver_UnknownScenarioRejected validates that the driver
// fails fast on misconfigured runs.
func TestHTTPDriver_UnknownScenarioRejected(t *testing.T) {
	ts := newControlPlane(t)
	drv, err := harness.NewHTTPDriver(ts.URL, canonicalTokens())
	if err != nil {
		t.Fatalf("NewHTTPDriver: %v", err)
	}
	if _, _, err := drv.Setup("does-not-exist-anywhere"); err == nil {
		t.Fatal("Setup with unknown scenario should fail")
	}
}

// helpers ----------------------------------------------------------

func newControlPlane(t *testing.T) *httptest.Server {
	t.Helper()
	auth := server.NewStaticTokens(map[string]server.Identity{})
	srv, err := server.New(server.Config{
		ListenAddr: "127.0.0.1:0",
		Auth:       auth,
	})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	plane := control.New(srv, auth, "ws://test.invalid/lsdp.v1")
	ts := httptest.NewServer(plane.Mux())
	t.Cleanup(ts.Close)
	return ts
}

func canonicalTokens() map[string]string {
	return map[string]string{
		"$TOKEN_OPERATOR": "tok-op",
		"$TOKEN_VIEWER":   "tok-vw",
		"$TOKEN_SERVICE":  "tok-svc",
		"$TOKEN_TEST":     "tok-test",
	}
}
