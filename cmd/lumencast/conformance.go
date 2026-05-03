package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Lumencast/lumencast-go/conformance"
	"github.com/Lumencast/lumencast-go/interop/harness"
)

func cmdConformance(args []string) int {
	fs := flag.NewFlagSet("conformance", flag.ExitOnError)
	serverURL := fs.String("server", "", "ws://host:port/lsdp.v1 endpoint of the server under test")
	controlURL := fs.String("control-url", "", "http://host:port endpoint of the test control plane (interop mode)")
	tokenList := fs.String("tokens", "", "comma-separated NAME=VALUE pairs (e.g. $TOKEN_OPERATOR=mytok,$TOKEN_VIEWER=v)")
	tag := fs.String("tag", "required", "scenario tag filter: required, recommended, extended")
	timeout := fs.Duration("timeout", 60*time.Second, "total run timeout")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *serverURL == "" && *controlURL == "" {
		fmt.Fprintln(os.Stderr, "lumencast conformance: --server or --control-url required")
		return 2
	}

	tokens := parseTokens(*tokenList)

	opts := conformance.CLIRunOptions{
		ServerURL: *serverURL,
		Tokens:    tokens,
		TagFilter: conformance.Tag(*tag),
		Out:       os.Stdout,
		Timeout:   *timeout,
	}

	// Interop mode : when --control-url is set, build an HTTPDriver
	// and run with full setup/state piloting against the remote
	// control plane. Tokens become the canonical placeholder map ;
	// callers SHOULD pass the well-known interop tokens via --tokens
	// or rely on the canonical fixture from
	// `lumencast-protocol/interop/fixtures/canonical-tokens.json`.
	if *controlURL != "" {
		if len(tokens) == 0 {
			tokens = canonicalInteropTokens()
			opts.Tokens = tokens
		}
		drv, err := harness.NewHTTPDriver(*controlURL, tokens)
		if err != nil {
			fmt.Fprintf(os.Stderr, "lumencast conformance: build driver: %v\n", err)
			return 1
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := drv.HealthCheck(ctx); err != nil {
			cancel()
			fmt.Fprintf(os.Stderr, "lumencast conformance: control plane unreachable: %v\n", err)
			return 1
		}
		cancel()
		opts.Driver = drv
		// Driver supplies the URL; ServerURL becomes a fallback only.
		opts.ServerURL = ""

		// Skip the same scenarios the Go in-process self-test skips
		// — they require driver-side machinery the control plane
		// does not yet expose (multi-connection token rotation,
		// driver-driven mid-flight Emit hooks). Tracked as gaps in
		// `interop/CONTROL.md` follow-up.
		opts.SkipScenarios = append(opts.SkipScenarios,
			"token-rotation-no-flicker",
			"subscribe-snapshot-delta",
		)
	}

	rep, err := conformance.CLIRun(context.Background(), opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	if rep != nil && rep.Failed > 0 {
		return 1
	}
	if err != nil {
		return 1
	}
	return 0
}

func parseTokens(list string) map[string]string {
	out := map[string]string{}
	if list == "" {
		return out
	}
	for _, kv := range strings.Split(list, ",") {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			continue
		}
		out[strings.TrimSpace(kv[:eq])] = strings.TrimSpace(kv[eq+1:])
	}
	return out
}

// canonicalInteropTokens returns the placeholder→value mapping used
// across the cross-language interop matrix. Mirrored from
// `lumencast-protocol/interop/fixtures/canonical-tokens.json`. Kept
// inline here so the CLI can default sanely without a runtime file
// dependency.
func canonicalInteropTokens() map[string]string {
	return map[string]string{
		"$TOKEN_OPERATOR": "interop-tok-operator-7f3a",
		"$TOKEN_VIEWER":   "interop-tok-viewer-7f3a",
		"$TOKEN_SERVICE":  "interop-tok-service-7f3a",
		"$TOKEN_TEST":     "interop-tok-test-7f3a",
		"$TOKEN_INVALID":  "interop-tok-invalid-7f3a",
	}
}
