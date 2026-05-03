// Package harness implements the client-side companion to the interop
// test control plane (`lumencast-protocol/interop/CONTROL.md`).
//
// HTTPDriver lets the Go conformance suite drive any LSDP/1 server
// that exposes the `/test/*` HTTP endpoints, including servers
// implemented in other languages. It is a `conformance.Driver` whose
// Setup / SnapshotState calls translate to HTTP round-trips against
// the control plane URL supplied at construction.
//
// Production callers MUST NOT use this package — it depends on the
// remote server speaking the test control plane, which is itself an
// off-by-default test surface.
package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Lumencast/lumencast-go/conformance"
)

// HTTPDriver implements conformance.Driver against a remote LSDP/1
// server's test control plane. Construct with NewHTTPDriver, pass to
// conformance.Config.Driver.
type HTTPDriver struct {
	controlURL string
	client     *http.Client

	// tokens map is the canonical $TOKEN_* → value mapping shared
	// across every scenario in a run.
	tokens map[string]string

	// scenarios is loaded once at construction so Setup can derive
	// /test/setup payloads from each scenario's declared bundles
	// and expected initial state.
	scenarios map[string]*conformance.Scenario
}

// NewHTTPDriver returns a Driver that talks to controlURL (e.g.
// "http://127.0.0.1:9000"). The token map MUST cover every
// placeholder used by the scenarios (canonical set in
// `interop/fixtures/canonical-tokens.json`). Returns an error if the
// embedded scenario set fails to load.
func NewHTTPDriver(controlURL string, tokens map[string]string) (*HTTPDriver, error) {
	scs, err := conformance.Load()
	if err != nil {
		return nil, fmt.Errorf("harness: load scenarios: %w", err)
	}
	idx := make(map[string]*conformance.Scenario, len(scs))
	for _, sc := range scs {
		if _, err := sc.ComputeBundleHashes(); err != nil {
			return nil, fmt.Errorf("harness: hash bundles for %s: %w", sc.Name, err)
		}
		idx[sc.Name] = sc
	}
	cp := make(map[string]string, len(tokens))
	for k, v := range tokens {
		cp[k] = v
	}
	return &HTTPDriver{
		controlURL: controlURL,
		client:     &http.Client{Timeout: 5 * time.Second},
		tokens:     cp,
		scenarios:  idx,
	}, nil
}

// HealthCheck verifies the control plane is reachable and reports
// the expected version. Useful as a pre-flight before running the
// suite — the harness fails fast if the URL is wrong instead of
// failing every scenario the same way.
func (d *HTTPDriver) HealthCheck(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.controlURL+"/test/health", nil)
	if err != nil {
		return fmt.Errorf("health: %w", err)
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("health: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health: HTTP %d", resp.StatusCode)
	}
	var body struct {
		Status              string `json:"status"`
		ControlPlaneVersion int    `json:"control_plane_version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return fmt.Errorf("health: decode: %w", err)
	}
	if body.Status != "ok" {
		return fmt.Errorf("health: status=%q", body.Status)
	}
	if body.ControlPlaneVersion != 1 {
		return fmt.Errorf("harness: control_plane_version=%d, want 1", body.ControlPlaneVersion)
	}
	return nil
}

// Setup implements conformance.Driver. Sends POST /test/setup with the
// scenario's bundles + extracted initial state, returns the WebSocket
// URL the server allocates.
func (d *HTTPDriver) Setup(scenarioName string) (string, map[string]string, error) {
	sc, ok := d.scenarios[scenarioName]
	if !ok {
		return "", nil, fmt.Errorf("harness: unknown scenario %q", scenarioName)
	}

	payload := setupRequest{
		Scenario:     scenarioName,
		Tokens:       d.tokens,
		Bundles:      bundlesFor(sc),
		InitialState: extractInitialState(sc),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", nil, fmt.Errorf("harness: marshal setup: %w", err)
	}

	resp, err := d.post("/test/setup", body)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", nil, decodeProblem(resp, "setup")
	}
	var sresp setupResponse
	if err := json.NewDecoder(resp.Body).Decode(&sresp); err != nil {
		return "", nil, fmt.Errorf("harness: decode setup: %w", err)
	}
	if sresp.WSUrl == "" {
		return "", nil, errors.New("harness: setup returned empty ws_url")
	}
	return sresp.WSUrl, d.tokens, nil
}

// SnapshotState implements conformance.Driver. GET /test/state.
//
// Returns nil on transport / decode errors — the harness logs an
// expect-server-state mismatch then, which is the right failure mode
// (you wanted to assert against state, the server didn't give us
// state, that's a failed assertion).
func (d *HTTPDriver) SnapshotState() map[string]any {
	req, err := http.NewRequest(http.MethodGet, d.controlURL+"/test/state", nil)
	if err != nil {
		return nil
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var sresp stateResponse
	if err := json.NewDecoder(resp.Body).Decode(&sresp); err != nil {
		return nil
	}
	return sresp.State
}

// Reset clears server state between runs. Optional ; the harness's
// next Setup already triggers a server-side reset internally. Exposed
// for tools that want explicit control (e.g. `--scenario X` flag
// followed by tear-down).
func (d *HTTPDriver) Reset() error {
	resp, err := d.post("/test/reset", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return decodeProblem(resp, "reset")
	}
	return nil
}

// Emit triggers a server-driven delta via POST /test/emit. Used by
// scenarios that script server-sends AFTER a client-sends without
// going through the server's normal input path (e.g. scenarios that
// stage non-input-driven deltas).
func (d *HTTPDriver) Emit(patches []map[string]any) error {
	body, err := json.Marshal(map[string]any{"patches": patches})
	if err != nil {
		return err
	}
	resp, err := d.post("/test/emit", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return decodeProblem(resp, "emit")
	}
	return nil
}

// internals ----------------------------------------------------------

func (d *HTTPDriver) post(path string, body []byte) (*http.Response, error) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequest(http.MethodPost, d.controlURL+path, rdr)
	if err != nil {
		return nil, fmt.Errorf("harness: build %s: %w", path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("harness: %s: %w", path, err)
	}
	return resp, nil
}

func decodeProblem(resp *http.Response, op string) error {
	var p struct {
		Title  string `json:"title"`
		Status int    `json:"status"`
		Detail string `json:"detail"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&p)
	if p.Detail != "" {
		return fmt.Errorf("harness: %s HTTP %d: %s", op, resp.StatusCode, p.Detail)
	}
	return fmt.Errorf("harness: %s HTTP %d", op, resp.StatusCode)
}

// setupRequest mirrors the body shape from CONTROL.md § POST /test/setup.
//
// Note on omitempty : nil maps marshal as JSON `null`. Older / stricter
// SDK control planes reject `null` with 422 Unprocessable Entity for
// fields they expect as objects. omitempty makes the field absent
// instead, matching the spec's "MUST accept null, omitted, or empty
// for any of these fields" robustness clause.
type setupRequest struct {
	Scenario     string            `json:"scenario"`
	Tokens       map[string]string `json:"tokens,omitempty"`
	Bundles      []setupBundle     `json:"bundles,omitempty"`
	InitialState map[string]any    `json:"initial_state,omitempty"`
}

type setupBundle struct {
	ID     string         `json:"id"`
	Hash   string         `json:"hash"`
	Inline map[string]any `json:"inline"`
}

type setupResponse struct {
	WSUrl        string `json:"ws_url"`
	SceneID      string `json:"scene_id"`
	SceneVersion string `json:"scene_version"`
}

type stateResponse struct {
	SceneID      string         `json:"scene_id"`
	SceneVersion string         `json:"scene_version"`
	State        map[string]any `json:"state"`
}

// bundlesFor maps a scenario's declared bundles into the control-plane
// payload. Scenarios that omit bundles are treated as needing a
// synthetic single-scene seed derived from the first server-sends
// snapshot — see extractInitialState for the matching state.
//
// Note on the scene id : when a scenario declares a bundle, the
// bundle's `id` is the SCENARIO-LOCAL reference name (used in
// `$BUNDLE.<id>.hash` placeholders), while the inline LSML carries
// the actual scene identifier. The server registers under the LSML
// scene_id so emitted snapshots match scenario expectations.
func bundlesFor(sc *conformance.Scenario) []setupBundle {
	if len(sc.Bundles) > 0 {
		out := make([]setupBundle, len(sc.Bundles))
		for i, b := range sc.Bundles {
			id := b.ID
			if v, ok := b.Inline["scene_id"].(string); ok && v != "" {
				id = v
			}
			out[i] = setupBundle{
				ID:     id,
				Hash:   b.Hash(),
				Inline: b.Inline,
			}
		}
		return out
	}
	// Synthetic bundle. Prefer the literal scene_id from the first
	// server-sends snapshot ; scenarios that fix scene_version use
	// a literal hash rather than $ANY_HASH, in which case we match
	// the server hint to that hash so the snapshot frame matches.
	id, hash := firstSceneIDAndHash(sc)
	if id == "" {
		id = sc.Name
	}
	if hash == "" {
		hash = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	}
	// Derive operator_inputs from any `__inputs.*` keys in the
	// expected initial state. Scenarios like unknown-path-rejected
	// rely on the server enforcing path declaredness ; without an
	// explicit bundle we synthesise the minimum metadata to make
	// that enforcement work.
	inline := map[string]any{}
	if state := extractInitialState(sc); len(state) > 0 {
		var inputs []any
		for path := range state {
			if !startsWith(path, "__inputs.") {
				continue
			}
			inputs = append(inputs, map[string]any{
				"path": path,
			})
		}
		if len(inputs) > 0 {
			inline["operator_inputs"] = inputs
		}
	}
	return []setupBundle{{
		ID:     id,
		Hash:   hash,
		Inline: inline,
	}}
}

func startsWith(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

func firstSceneIDAndHash(sc *conformance.Scenario) (string, string) {
	var id, hash string
	for _, step := range sc.Steps {
		if step.Kind != conformance.StepServerSends {
			continue
		}
		if id == "" {
			if v, ok := step.Frame["scene_id"].(string); ok && v != "" && v != "$ANY" {
				id = v
			}
		}
		if hash == "" {
			if v, ok := step.Frame["scene_version"].(string); ok && v != "" && v != "$ANY_HASH" {
				hash = v
			}
		}
		if id != "" && hash != "" {
			return id, hash
		}
	}
	return id, hash
}

// extractInitialState pulls the `state` map out of the first
// server-sends snapshot in the scenario. That state is what the
// server MUST be primed with so its first emitted snapshot matches
// the scenario expectation. Scenarios with no snapshot (auth-denied,
// envelope rejection, …) get no initial state and the server starts
// empty.
func extractInitialState(sc *conformance.Scenario) map[string]any {
	for _, step := range sc.Steps {
		if step.Kind != conformance.StepServerSends {
			continue
		}
		t, _ := step.Frame["type"].(string)
		if t != "snapshot" {
			continue
		}
		state, _ := step.Frame["state"].(map[string]any)
		if state == nil {
			return nil
		}
		// Strip $ANY sentinels — we can't seed a server with literal
		// "$ANY" as a state value.
		out := make(map[string]any, len(state))
		for k, v := range state {
			if s, ok := v.(string); ok && (s == "$ANY" || s == "$ANY_HASH") {
				continue
			}
			out[k] = v
		}
		return out
	}
	return nil
}
