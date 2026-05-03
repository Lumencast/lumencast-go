package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Lumencast/lumencast-go/protocol"
)

// Config drives the harness. Either ServerURL (external server) or
// Driver (in-process server) MUST be set ; both is invalid.
type Config struct {
	// ServerURL is a ws://host:port/lsdp.v1 endpoint to drive against.
	// The harness opens one WebSocket per scenario.
	ServerURL string

	// Driver is the in-process bridge. The harness calls
	// Driver.Setup(scenarioName) before every scenario to acquire a
	// fresh server URL plus the canonical token map.
	Driver Driver

	// TagFilter, when set, restricts execution to scenarios with the
	// given tag (TagRequired by default).
	TagFilter Tag

	// Tokens maps the scenario placeholder strings ($TOKEN_OPERATOR,
	// etc.) to live tokens recognised by the server under test. The
	// canonical placeholders :
	//
	//   $TOKEN_OPERATOR
	//   $TOKEN_VIEWER
	//   $TOKEN_SERVICE
	//   $TOKEN_TEST
	//   $TOKEN_INVALID
	//
	// If Driver is set, the driver's tokens take precedence.
	Tokens map[string]string

	// SkipScenarios names scenarios to skip (e.g. unsupported targets).
	SkipScenarios []string
}

// Driver is the in-process bridge. Tests that exercise the kit's own
// server pass an instance of (*server.Server)Driver here.
type Driver interface {
	// Setup resets state (clears scenes, recreates the active scene)
	// and returns a fresh ws://.../lsdp.v1 URL plus the token map for
	// the placeholders.
	Setup(scenarioName string) (url string, tokens map[string]string, err error)

	// SnapshotState returns the active scene's authoritative state,
	// used by expect-server-state. Map keys are dotted leaf paths.
	SnapshotState() map[string]any
}

// BundleAwareDriver is an optional extension. Drivers that implement
// it receive the canonical hashes of every inline bundle declared on
// the scenario, so they can pin scene_version on the registered scene
// and pre-populate defaults.
type BundleAwareDriver interface {
	Driver
	OnBundles(scenarioName string, bundles []ResolvedBundle) error
}

// EmittingDriver is an optional extension required by scenarios that
// use the `server-emits` step kind. The harness calls Emit(patches)
// to trigger a server-driven delta before reading the next frame.
//
// In-process drivers wired to a real server typically forward to
// Scene.Emit ; HTTP-driven drivers POST to the control plane's
// /test/emit endpoint.
type EmittingDriver interface {
	Driver
	Emit(patches []map[string]any) error
}

// ResolvedBundle is the Driver-friendly view of a scenario bundle :
// the inline LSML body plus the computed sha256:<hex> hash.
type ResolvedBundle struct {
	ID     string
	Hash   string
	Inline map[string]any
}

// Report summarises a Run.
type Report struct {
	Total   int
	Passed  int
	Failed  int
	Skipped int
	Results []Result
}

// Result is the per-scenario outcome.
type Result struct {
	Name    string
	Tag     Tag
	Target  Target
	Passed  bool
	Skipped bool
	Reason  string
	Err     error
}

// Run executes every embedded scenario matching cfg.TagFilter and
// returns a Report. When called from a *testing.T (non-nil), it also
// emits sub-tests via t.Run so failures land in the standard test
// output.
func Run(t *testing.T, cfg Config) *Report {
	scenarios, err := Load()
	if err != nil {
		if t != nil {
			t.Fatalf("conformance: load: %v", err)
		}
		return &Report{}
	}

	skip := make(map[string]struct{}, len(cfg.SkipScenarios))
	for _, n := range cfg.SkipScenarios {
		skip[n] = struct{}{}
	}

	tag := cfg.TagFilter
	if tag == "" {
		tag = TagRequired
	}

	rep := &Report{}
	for _, sc := range scenarios {
		rep.Total++
		if _, sk := skip[sc.Name]; sk {
			rep.Skipped++
			rep.Results = append(rep.Results, Result{
				Name: sc.Name, Tag: sc.Tag, Target: sc.Target,
				Skipped: true, Reason: "skipped via SkipScenarios",
			})
			continue
		}
		if sc.Tag != tag && tag != "" && !(tag == TagRequired && sc.Tag == TagRequired) {
			// Filter mismatch — skip silently.
			rep.Skipped++
			continue
		}
		// Skip target=runtime in this Go server harness (no Go runtime).
		if sc.Target == TargetRuntime {
			rep.Skipped++
			rep.Results = append(rep.Results, Result{
				Name: sc.Name, Tag: sc.Tag, Target: sc.Target,
				Skipped: true, Reason: "runtime-targeted scenario, harness drives a server",
			})
			continue
		}

		runOne := func() {
			err := runScenario(sc, cfg)
			res := Result{Name: sc.Name, Tag: sc.Tag, Target: sc.Target, Err: err, Passed: err == nil}
			if err != nil {
				rep.Failed++
			} else {
				rep.Passed++
			}
			rep.Results = append(rep.Results, res)
		}

		if t != nil {
			t.Run(sc.Name, func(sub *testing.T) {
				err := runScenario(sc, cfg)
				if err != nil {
					sub.Fatalf("scenario %s failed: %v", sc.Name, err)
				}
				rep.Passed++
				rep.Results = append(rep.Results, Result{
					Name: sc.Name, Tag: sc.Tag, Target: sc.Target, Passed: true,
				})
			})
		} else {
			runOne()
		}
	}
	return rep
}

// runScenario executes one scenario by opening a WebSocket against the
// configured server and replaying the steps.
func runScenario(sc *Scenario, cfg Config) error {
	// Hash inline bundles before driver setup, so a BundleAware driver
	// can pin scene_version on the scene it builds for the scenario.
	bundleHashes, err := sc.ComputeBundleHashes()
	if err != nil {
		return fmt.Errorf("bundles: %w", err)
	}

	url, tokens, err := bootstrap(sc.Name, cfg)
	if err != nil {
		return fmt.Errorf("setup: %w", err)
	}

	if cfg.Driver != nil {
		if ba, ok := cfg.Driver.(BundleAwareDriver); ok && len(sc.Bundles) > 0 {
			resolved := make([]ResolvedBundle, len(sc.Bundles))
			for i, b := range sc.Bundles {
				resolved[i] = ResolvedBundle{ID: b.ID, Hash: b.hash, Inline: b.Inline}
			}
			if err := ba.OnBundles(sc.Name, resolved); err != nil {
				return fmt.Errorf("driver bundles: %w", err)
			}
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	c, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{
		Subprotocols: []string{protocol.SubProtocol},
		HTTPClient:   &http.Client{Timeout: 5 * time.Second},
	})
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer func() { _ = c.Close(websocket.StatusNormalClosure, "") }()

	exec := &exec{
		c:            c,
		ctx:          ctx,
		tokens:       tokens,
		bundleHashes: bundleHashes,
		driver:       cfg.Driver,
	}

	for i, step := range sc.Steps {
		if err := exec.runStep(sc, step); err != nil {
			return fmt.Errorf("step %d (%s): %w", i+1, step.Kind, err)
		}
	}
	return nil
}

// bootstrap resolves the URL + token map for a scenario : driver if
// set, else cfg.ServerURL + cfg.Tokens.
func bootstrap(name string, cfg Config) (string, map[string]string, error) {
	if cfg.Driver != nil {
		return cfg.Driver.Setup(name)
	}
	if cfg.ServerURL == "" {
		return "", nil, errors.New("Config requires either Driver or ServerURL")
	}
	tokens := cfg.Tokens
	if tokens == nil {
		tokens = map[string]string{}
	}
	return cfg.ServerURL, tokens, nil
}

// exec carries per-scenario runtime state.
type exec struct {
	c            *websocket.Conn
	ctx          context.Context
	tokens       map[string]string
	bundleHashes map[string]string
	driver       Driver

	// runtimeState is a stand-in store for expect-runtime-state when
	// running against a server-only harness. It mirrors the snapshots
	// + deltas the runner observes from the server.
	runtimeState map[string]json.RawMessage
}

func (e *exec) runStep(sc *Scenario, step Step) error {
	switch step.Kind {
	case StepClientSends:
		return e.send(step.Frame)
	case StepServerSends:
		return e.expectServer(step.Frame)
	case StepServerEmits:
		return e.serverEmits(step.Frame)
	case StepExpectRuntimeState:
		return e.expectRuntime(step.State)
	case StepExpectServerState:
		return e.expectServerState(step.State)
	case StepExpectNoFrameFor:
		return e.expectQuiet(time.Duration(step.DurationMs) * time.Millisecond)
	case StepExpectClientAction:
		return e.expectClientAction(step.Action, step.Reason)
	default:
		return fmt.Errorf("unsupported step kind %q", step.Kind)
	}
}

// serverEmits triggers the driver to make the server emit the
// expected frame, then validates the actual wire frame against the
// template. Today only frame.type == "delta" is supported (via
// /test/emit on the control plane) ; other types are reserved for
// future control plane extensions.
func (e *exec) serverEmits(expected map[string]any) error {
	t, _ := expected["type"].(string)
	if t != "delta" {
		return fmt.Errorf("server-emits only supports type=delta today, got %q", t)
	}
	emitter, ok := e.driver.(EmittingDriver)
	if !ok {
		return errors.New("server-emits step requires a driver implementing EmittingDriver")
	}
	patches, ok := expected["patches"].([]any)
	if !ok {
		return errors.New("server-emits delta missing 'patches' list")
	}
	resolved := make([]map[string]any, 0, len(patches))
	for _, p := range patches {
		pm, ok := p.(map[string]any)
		if !ok {
			return fmt.Errorf("server-emits: patch is not a map: %v", p)
		}
		resolved = append(resolved, substitutePlaceholders(pm, e.tokens, e.bundleHashes).(map[string]any))
	}
	if err := emitter.Emit(resolved); err != nil {
		return fmt.Errorf("server-emits: driver emit: %w", err)
	}
	return e.expectServer(expected)
}

// send substitutes $TOKEN_* and $BUNDLE.* placeholders in the frame
// and writes one JSON text frame.
func (e *exec) send(frame map[string]any) error {
	resolved := substitutePlaceholders(frame, e.tokens, e.bundleHashes)
	raw, err := json.Marshal(resolved)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	wctx, cancel := context.WithTimeout(e.ctx, 2*time.Second)
	defer cancel()
	return e.c.Write(wctx, websocket.MessageText, raw)
}

// expectServer reads the next text frame and matches it against the
// expected template (with placeholder substitution applied).
func (e *exec) expectServer(expected map[string]any) error {
	rctx, cancel := context.WithTimeout(e.ctx, 2*time.Second)
	defer cancel()
	_, raw, err := e.c.Read(rctx)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}
	var actual map[string]any
	if err := json.Unmarshal(raw, &actual); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	resolvedExpected, _ := substitutePlaceholders(expected, e.tokens, e.bundleHashes).(map[string]any)
	if err := matchFrame(resolvedExpected, actual); err != nil {
		return fmt.Errorf("frame mismatch: %w (got %s)", err, raw)
	}
	// Mirror snapshot/delta into the runner's runtime-state shadow so
	// expect-runtime-state can verify in server-only mode.
	e.absorbServerFrame(actual)
	return nil
}

func (e *exec) absorbServerFrame(frame map[string]any) {
	t, _ := frame["type"].(string)
	switch t {
	case protocol.TypeSnapshot:
		state, _ := frame["state"].(map[string]any)
		e.runtimeState = make(map[string]json.RawMessage, len(state))
		for k, v := range state {
			b, _ := json.Marshal(v)
			e.runtimeState[k] = b
		}
	case protocol.TypeDelta:
		patches, _ := frame["patches"].([]any)
		if e.runtimeState == nil {
			e.runtimeState = map[string]json.RawMessage{}
		}
		for _, p := range patches {
			pm, ok := p.(map[string]any)
			if !ok {
				continue
			}
			path, _ := pm["path"].(string)
			b, _ := json.Marshal(pm["value"])
			e.runtimeState[path] = b
		}
	}
}

func (e *exec) expectRuntime(want map[string]any) error {
	for k, v := range want {
		got, ok := e.runtimeState[k]
		if !ok {
			return fmt.Errorf("runtime state missing %q", k)
		}
		var actual any
		if err := json.Unmarshal(got, &actual); err != nil {
			return err
		}
		if err := matchValue(v, actual, k); err != nil {
			return err
		}
	}
	return nil
}

func (e *exec) expectServerState(want map[string]any) error {
	if e.driver == nil {
		// No introspection available — fall back to checking the
		// runtime-state shadow we maintain from snapshots/deltas.
		return e.expectRuntime(want)
	}
	state := e.driver.SnapshotState()
	for k, v := range want {
		got, ok := state[k]
		if !ok {
			return fmt.Errorf("server state missing %q", k)
		}
		if err := matchValue(v, got, k); err != nil {
			return err
		}
	}
	return nil
}

func (e *exec) expectQuiet(d time.Duration) error {
	rctx, cancel := context.WithTimeout(e.ctx, d)
	defer cancel()
	_, raw, err := e.c.Read(rctx)
	if err == nil {
		return fmt.Errorf("expected silence for %v, got frame: %s", d, raw)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		// Quiet window elapsed without a frame — success.
		return nil
	}
	// Per SCENARIO-FORMAT.md (`expect-no-frame-for` § Connection-close
	// semantics), a clean server-initiated close within the duration is
	// success — no data flowed. Abnormal closures remain failures.
	var ce websocket.CloseError
	if errors.As(err, &ce) {
		switch ce.Code {
		case websocket.StatusNormalClosure,
			websocket.StatusGoingAway,
			websocket.StatusNoStatusRcvd:
			return nil
		}
	}
	return fmt.Errorf("read error during quiet window: %w", err)
}

func (e *exec) expectClientAction(action ClientAction, _ string) error {
	switch action {
	case ActionCloseWithReason, ActionReconnect:
		// In runtime-targeted scenarios (which we skip), the runner
		// would observe the runtime's behaviour. In server mode, a
		// close-with-reason translates to "the server should close
		// the connection". Read should return an error.
		rctx, cancel := context.WithTimeout(e.ctx, 2*time.Second)
		defer cancel()
		_, _, err := e.c.Read(rctx)
		if err == nil {
			return errors.New("expected connection close, got frame")
		}
		return nil
	default:
		return fmt.Errorf("unknown client action %q", action)
	}
}

// substitutePlaceholders returns a copy of frame with $TOKEN_* and
// $BUNDLE.<id>.hash string values replaced. Unknown placeholders pass
// through verbatim — the server will reject them, which some scenarios
// rely on (auth-denied-closes uses $TOKEN_INVALID).
//
// Bundle hash placeholders : "$BUNDLE.<id>.hash" → "sha256:<hex>".
// Bundle hashes is the map produced by Scenario.ComputeBundleHashes().
func substitutePlaceholders(in any, tokens, bundleHashes map[string]string) any {
	switch v := in.(type) {
	case string:
		if strings.HasPrefix(v, "$TOKEN_") {
			if t, ok := tokens[v]; ok {
				return t
			}
			return v
		}
		if strings.HasPrefix(v, "$BUNDLE.") && strings.HasSuffix(v, ".hash") {
			id := strings.TrimSuffix(strings.TrimPrefix(v, "$BUNDLE."), ".hash")
			if h, ok := bundleHashes[id]; ok {
				return h
			}
			return v
		}
		return v
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, val := range v {
			out[k] = substitutePlaceholders(val, tokens, bundleHashes)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, val := range v {
			out[i] = substitutePlaceholders(val, tokens, bundleHashes)
		}
		return out
	default:
		return v
	}
}
