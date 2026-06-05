//go:build conformance

package conformance_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Lumencast/lumencast-go/conformance"
	"github.com/Lumencast/lumencast-go/protocol"
	"github.com/Lumencast/lumencast-go/server"
)

// headerTrustDriver runs the full conformance suite against a server
// configured with the header-trust identity seam (ADR 007 §C.3a) instead
// of token authentication. It embeds inProcessDriver to reuse all of the
// scene setup / bundle / emit / snapshot plumbing, and overrides only the
// pieces that change : the server is built with IdentityFromRequest (no
// Authenticator at all), and the per-scenario principal is conveyed via an
// upgrade header (DialHeader) rather than the Subscribe token.
//
// This proves the seam is conformant for target:server (and target:any)
// scenarios : the same wire lifecycle the token path satisfies, satisfied
// when identity comes from the request instead of the token.
type headerTrustDriver struct {
	*inProcessDriver
}

// hdrFor maps a scenario's single token placeholder to a test header
// principal. The seam (hdrIdentity) maps that header back to an Identity.
// auth-denied-closes deliberately maps to an unauthenticated principal so
// the seam still produces AUTH_DENIED, exactly as the token path does for
// $TOKEN_INVALID.
func hdrFor(scenario string) string {
	switch scenario {
	case "viewer-cannot-input":
		return "viewer"
	case "test-session-namespace":
		return "test"
	case "auth-denied-closes":
		return "" // unauthenticated → AUTH_DENIED
	default:
		return "operator"
	}
}

// hdrIdentity is the header-trust seam : it derives the Identity from the
// upgrade request's X-Conformance-Principal header. An absent/unknown
// principal is anonymous, which the server rejects with AUTH_DENIED.
func hdrIdentity(r *http.Request) (server.Identity, error) {
	switch r.Header.Get("X-Conformance-Principal") {
	case "operator":
		return server.Identity{Subject: "op", Role: protocol.RoleOperator}, nil
	case "viewer":
		return server.Identity{Subject: "v", Role: protocol.RoleViewer}, nil
	case "service":
		return server.Identity{Subject: "svc", Role: protocol.RoleService, Paths: []string{"__inputs.*"}}, nil
	case "test":
		return server.Identity{Subject: "tst", Role: protocol.RoleTest}, nil
	default:
		return server.Anonymous(), errors.New("no conformance principal")
	}
}

func newHeaderTrustDriver(t *testing.T) *headerTrustDriver {
	t.Helper()
	d := &headerTrustDriver{inProcessDriver: &inProcessDriver{}}
	srv, err := server.New(server.Config{
		ListenAddr:          "127.0.0.1:0",
		IdentityFromRequest: hdrIdentity, // no Auth — header-trust only
		PingInterval:        30 * time.Second,
		SubscribeTimeout:    2 * time.Second,
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

// DialHeader implements conformance.DialHeaderDriver : it injects the
// per-scenario principal header the seam reads.
func (d *headerTrustDriver) DialHeader(scenarioName string) http.Header {
	principal := hdrFor(scenarioName)
	if principal == "" {
		return nil
	}
	h := http.Header{}
	h.Set("X-Conformance-Principal", principal)
	return h
}

func TestConformanceSuite_HeaderTrust(t *testing.T) {
	driver := newHeaderTrustDriver(t)
	conformance.Run(t, conformance.Config{
		Driver:    driver,
		TagFilter: conformance.TagRequired,
	})
}
