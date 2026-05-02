package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Lumencast/lumencast-go/conformance"
)

func cmdConformance(args []string) int {
	fs := flag.NewFlagSet("conformance", flag.ExitOnError)
	serverURL := fs.String("server", "", "ws://host:port/lsdp.v1 endpoint of the server under test")
	tokenList := fs.String("tokens", "", "comma-separated NAME=VALUE pairs (e.g. $TOKEN_OPERATOR=mytok,$TOKEN_VIEWER=v)")
	tag := fs.String("tag", "required", "scenario tag filter: required, recommended, extended")
	timeout := fs.Duration("timeout", 60*time.Second, "total run timeout")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *serverURL == "" {
		fmt.Fprintln(os.Stderr, "lumencast conformance: --server required")
		return 2
	}

	tokens := parseTokens(*tokenList)

	rep, err := conformance.CLIRun(context.Background(), conformance.CLIRunOptions{
		ServerURL: *serverURL,
		Tokens:    tokens,
		TagFilter: conformance.Tag(*tag),
		Out:       os.Stdout,
		Timeout:   *timeout,
	})
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
