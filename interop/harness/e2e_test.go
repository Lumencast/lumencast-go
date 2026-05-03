package harness_test

import (
	"context"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Lumencast/lumencast-go/conformance"
	"github.com/Lumencast/lumencast-go/interop/control"
	"github.com/Lumencast/lumencast-go/interop/harness"
	"github.com/Lumencast/lumencast-go/server"
)

// TestE2E_GoHarness_Vs_GoServer exercises the full interop loop : a
// real LSDP/1 server fronted by the control plane is driven by the
// HTTPDriver through every required scenario. Mirrors the CI matrix's
// Go × Go cell ; failures here mean the matrix would also fail.
//
// This test is the empirical proof that the control plane is
// sufficient to drive the conformance suite cross-process. JS and RS
// chantiers can hold themselves to the same standard against this
// reference.
func TestE2E_GoHarness_Vs_GoServer(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e: -short")
	}

	auth := server.NewStaticTokens(map[string]server.Identity{})
	srv, err := server.New(server.Config{
		ListenAddr: "127.0.0.1:0",
		Auth:       auth,
	})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}

	// Run the LSDP/1 endpoint on httptest, then derive its host:port
	// and feed it back to the control plane as the canonical ws URL.
	wsTS := httptest.NewServer(srv.Mux())
	t.Cleanup(wsTS.Close)
	wsURL := strings.Replace(wsTS.URL, "http://", "ws://", 1) + "/lsdp.v1"

	plane := control.New(srv, auth, wsURL)
	ctlTS := httptest.NewServer(plane.Mux())
	t.Cleanup(ctlTS.Close)

	if _, err := url.Parse(ctlTS.URL); err != nil {
		t.Fatalf("control URL: %v", err)
	}

	tokens := map[string]string{
		"$TOKEN_OPERATOR": "tok-op",
		"$TOKEN_VIEWER":   "tok-vw",
		"$TOKEN_SERVICE":  "tok-svc",
		"$TOKEN_TEST":     "tok-test",
	}
	drv, err := harness.NewHTTPDriver(ctlTS.URL, tokens)
	if err != nil {
		t.Fatalf("NewHTTPDriver: %v", err)
	}
	if err := drv.HealthCheck(context.Background()); err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}

	cfg := conformance.Config{
		Driver:    drv,
		TagFilter: conformance.TagRequired,
		// token-rotation-no-flicker has target:runtime so it's
		// auto-skipped by the harness. subscribe-snapshot-delta
		// uses `server-emits` for its mid-flight deltas — the
		// HTTPDriver's Emit method routes them through /test/emit.
	}
	rep := conformance.Run(nil, cfg)
	if rep.Failed > 0 {
		var msgs []string
		for _, r := range rep.Results {
			if !r.Passed && !r.Skipped {
				msgs = append(msgs, r.Name+": "+r.Err.Error())
			}
		}
		t.Fatalf("e2e failed: %d/%d scenarios — %s",
			rep.Failed, rep.Total, strings.Join(msgs, " ; "))
	}
	if rep.Passed == 0 {
		t.Fatalf("e2e: no scenarios passed (rep=%+v)", rep)
	}
	t.Logf("e2e: %d passed, %d skipped (timing budget: %s)",
		rep.Passed, rep.Skipped, time.Since(time.Now()))
}
