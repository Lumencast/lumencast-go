package conformance

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"
)

// CLIRunOptions parameterises the CLI-friendly Run wrapper.
type CLIRunOptions struct {
	// ServerURL is required when Driver is nil.
	ServerURL string

	// Driver, when non-nil, takes precedence over ServerURL — every
	// scenario re-primes server state via Driver.Setup. Used by the
	// interop matrix (see lumencast-go/interop/harness).
	Driver Driver

	// Tokens supplied via flags (k=v pairs).
	Tokens map[string]string

	// TagFilter — defaults to TagRequired.
	TagFilter Tag

	// SkipScenarios names scenarios to skip (forwarded to Config).
	// Useful for callers that drive a server which doesn't yet
	// implement features required by certain scenarios (e.g.
	// driver-driven Emit hooks for subscribe-snapshot-delta).
	SkipScenarios []string

	// Output stream for the human-readable report.
	Out io.Writer

	// Timeout caps the total run time. Defaults to 60 s.
	Timeout time.Duration
}

// CLIRun executes the suite for the lumencast conformance subcommand.
// Returns a non-nil error iff one or more scenarios failed.
func CLIRun(ctx context.Context, opts CLIRunOptions) (*Report, error) {
	if opts.Timeout == 0 {
		opts.Timeout = 60 * time.Second
	}
	cfg := Config{
		ServerURL:     opts.ServerURL,
		Driver:        opts.Driver,
		Tokens:        opts.Tokens,
		TagFilter:     opts.TagFilter,
		SkipScenarios: opts.SkipScenarios,
	}
	if cfg.TagFilter == "" {
		cfg.TagFilter = TagRequired
	}

	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	_ = ctx // reserved for future per-scenario context plumbing

	rep := Run(nil, cfg)

	if opts.Out != nil {
		writeReport(opts.Out, rep)
	}

	if rep.Failed > 0 {
		return rep, fmt.Errorf("conformance: %d/%d scenarios failed", rep.Failed, rep.Total)
	}
	return rep, nil
}

func writeReport(w io.Writer, rep *Report) {
	fmt.Fprintf(w, "Conformance report — %d total, %d passed, %d failed, %d skipped\n",
		rep.Total, rep.Passed, rep.Failed, rep.Skipped)
	for _, r := range rep.Results {
		switch {
		case r.Passed:
			fmt.Fprintf(w, "  PASS  %s [%s/%s]\n", r.Name, r.Tag, r.Target)
		case r.Skipped:
			reason := r.Reason
			if reason == "" {
				reason = "filtered"
			}
			fmt.Fprintf(w, "  SKIP  %s — %s\n", r.Name, reason)
		default:
			msg := "(no error)"
			if r.Err != nil {
				msg = strings.ReplaceAll(r.Err.Error(), "\n", " ")
			}
			fmt.Fprintf(w, "  FAIL  %s [%s/%s] — %s\n", r.Name, r.Tag, r.Target, msg)
		}
	}
}
