// Package control implements the LSDP/1 interop test control plane.
//
// The control plane is an HTTP API that lets an external harness
// (typically running in a different language) drive deterministic
// conformance scenarios against a live lumencast-go server. The
// endpoints are specified in
// `lumencast-protocol/interop/CONTROL.md` and mirror the in-process
// `conformance.Driver` interface.
//
// This package is for test infrastructure only. Production code MUST
// NOT mount the control handler on a public-facing port.
package control

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/Lumencast/lumencast-go/lsml"
	"github.com/Lumencast/lumencast-go/protocol"
	"github.com/Lumencast/lumencast-go/server"
)

// Plane is the control plane. It owns the lumencast-go Server it
// drives, plus a StaticTokens authenticator for placeholder mapping.
type Plane struct {
	srv    *server.Server
	auth   *server.StaticTokens
	wsBase string // ws://host:port/lsdp.v1 — printed back in /test/setup

	mu     sync.Mutex
	active string // id of the most recent /test/setup scene, "" if none
}

// New wires a Plane around an existing server + StaticTokens. The
// caller retains ownership of both.
func New(srv *server.Server, auth *server.StaticTokens, wsBase string) *Plane {
	return &Plane{srv: srv, auth: auth, wsBase: wsBase}
}

// Mux returns an http.ServeMux mounting every /test/* endpoint.
func (p *Plane) Mux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/test/setup", p.handleSetup)
	mux.HandleFunc("/test/reset", p.handleReset)
	mux.HandleFunc("/test/state", p.handleState)
	mux.HandleFunc("/test/emit", p.handleEmit)
	mux.HandleFunc("/test/health", p.handleHealth)
	return mux
}

// setupRequest is the body of POST /test/setup.
type setupRequest struct {
	Scenario     string                  `json:"scenario"`
	Tokens       map[string]string       `json:"tokens"`
	Bundles      []setupBundle           `json:"bundles"`
	InitialState map[string]any          `json:"initial_state"`
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

func (p *Plane) handleSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeProblem(w, http.StatusMethodNotAllowed, "method-not-allowed", "POST required")
		return
	}
	var req setupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeProblem(w, http.StatusBadRequest, "bad-body", "invalid JSON: "+err.Error())
		return
	}
	if len(req.Bundles) == 0 {
		writeProblem(w, http.StatusBadRequest, "missing-bundle", "at least one bundle required")
		return
	}

	p.srv.Reset()
	p.installTokens(req.Tokens)

	primary := req.Bundles[0]
	// `inline.scene_id` overrides bundle.id when present (per
	// CONTROL.md "Inline bundle parsing"). The bundle.id is the
	// scenario-local reference name used in $BUNDLE.<id>.hash
	// placeholders ; the inline LSML carries the canonical
	// server-side scene identifier.
	effectiveID := primary.ID
	if v, ok := primary.Inline["scene_id"].(string); ok && v != "" {
		effectiveID = v
	}
	opts := []server.SceneOption{}
	if primary.Hash != "" {
		opts = append(opts, server.WithSceneVersion(primary.Hash))
	}
	if specs := extractInputSpecs(primary.Inline); len(specs) > 0 {
		opts = append(opts, server.WithOperatorInputs(specs))
	}
	scene := p.srv.NewScene(effectiveID, opts...)

	// Vendor-primitive validation (RFC-0001 + Amendment 2). A bundle
	// carrying a malformed `x-zab.capture` is registered but marked
	// unservable, so the subscriber gets an INVALID_VALUE error frame
	// where it would otherwise read a snapshot of a scene built from a
	// bundle we rejected. Scoped to capture nodes on purpose : the
	// scenario suite feeds inline bodies from several LSML minors, and a
	// full lsml.Validate here would reject primitives this SDK has not
	// caught up with yet.
	if layout, ok := primary.Inline["layout"]; ok {
		if err := lsml.CheckZabCaptureNodes(layout); err != nil {
			scene.Reject(protocol.CodeInvalidValue, err.Error())
		}
	}

	// initial_state takes precedence ; fall back to inline.defaults
	// for scenarios that declare a bundle with seeded values rather
	// than pre-extracted state. Either path leaves the server with
	// the scene populated before any client subscribes.
	state := req.InitialState
	if len(state) == 0 {
		if d, ok := primary.Inline["defaults"].(map[string]any); ok && len(d) > 0 {
			state = d
		}
	}
	if len(state) > 0 {
		if err := scene.Set(state); err != nil {
			writeProblem(w, http.StatusBadRequest, "bad-initial-state", err.Error())
			return
		}
	}
	if err := p.srv.SetActive(effectiveID); err != nil {
		writeProblem(w, http.StatusInternalServerError, "set-active", err.Error())
		return
	}

	// Register secondary bundles (for bundle-incompatible negotiation
	// or alternate scenes). They start empty ; harnesses populate via
	// /test/emit if needed. Same inline.scene_id override applies.
	for _, b := range req.Bundles[1:] {
		secID := b.ID
		if v, ok := b.Inline["scene_id"].(string); ok && v != "" {
			secID = v
		}
		sc := p.srv.NewScene(secID)
		if b.Hash != "" {
			sc.SetVersion(b.Hash)
		}
	}

	p.mu.Lock()
	p.active = effectiveID
	p.mu.Unlock()

	writeJSON(w, http.StatusOK, setupResponse{
		WSUrl:        p.wsBase,
		SceneID:      effectiveID,
		SceneVersion: primary.Hash,
	})
}

func (p *Plane) handleReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeProblem(w, http.StatusMethodNotAllowed, "method-not-allowed", "POST required")
		return
	}
	p.srv.Reset()
	p.auth.Reset()
	p.mu.Lock()
	p.active = ""
	p.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

type stateResponse struct {
	SceneID      string         `json:"scene_id"`
	SceneVersion string         `json:"scene_version"`
	State        map[string]any `json:"state"`
}

func (p *Plane) handleState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeProblem(w, http.StatusMethodNotAllowed, "method-not-allowed", "GET required")
		return
	}
	scene := p.srv.ActiveScene()
	if scene == nil {
		writeProblem(w, http.StatusConflict, "no-active-scene", "/test/setup not called since last /test/reset")
		return
	}
	raw := scene.SnapshotForConformance()
	out := make(map[string]any, len(raw))
	for k, v := range raw {
		var decoded any
		if err := json.Unmarshal(v, &decoded); err != nil {
			out[k] = string(v)
			continue
		}
		out[k] = decoded
	}
	writeJSON(w, http.StatusOK, stateResponse{
		SceneID:      scene.ID(),
		SceneVersion: scene.Version(),
		State:        out,
	})
}

type emitRequest struct {
	Patches []emitPatch `json:"patches"`
}

type emitPatch struct {
	Path  string `json:"path"`
	Value any    `json:"value"`
}

func (p *Plane) handleEmit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeProblem(w, http.StatusMethodNotAllowed, "method-not-allowed", "POST required")
		return
	}
	var req emitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeProblem(w, http.StatusBadRequest, "bad-body", "invalid JSON: "+err.Error())
		return
	}
	if len(req.Patches) == 0 {
		writeProblem(w, http.StatusBadRequest, "empty-patches", "at least one patch required")
		return
	}
	scene := p.srv.ActiveScene()
	if scene == nil {
		writeProblem(w, http.StatusConflict, "no-active-scene", "/test/setup not called")
		return
	}
	patches := make(map[string]any, len(req.Patches))
	for _, pt := range req.Patches {
		patches[pt.Path] = pt.Value
	}
	if err := scene.Emit(patches); err != nil {
		// Map known error codes to RFC 7807 problem responses.
		writeProblem(w, http.StatusBadRequest, errorCode(err), err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (p *Plane) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeProblem(w, http.StatusMethodNotAllowed, "method-not-allowed", "GET required")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":                  "ok",
		"control_plane_version":   1,
		"server":                  "lumencast-go",
	})
}

// installTokens replaces the StaticTokens contents with one entry per
// recognised placeholder. Unknown placeholders are ignored. The
// $TOKEN_INVALID placeholder is intentionally NOT installed — the
// LSDP/1 conformance suite expects auth to reject it.
func (p *Plane) installTokens(tokens map[string]string) {
	p.auth.Reset()
	for placeholder, value := range tokens {
		if placeholder == "$TOKEN_INVALID" || value == "" {
			continue
		}
		role, ok := placeholderRole(placeholder)
		if !ok {
			continue
		}
		id := server.Identity{
			Subject: placeholder,
			Role:    role,
		}
		p.auth.Set(value, id)
	}
}

// extractInputSpecs maps an inline LSML bundle's operator_inputs
// section into server.InputSpec values. Only the constraints the
// conformance scenarios use today are wired (type, maxLength) ;
// extend as more scenarios land.
func extractInputSpecs(inline map[string]any) []server.InputSpec {
	raw, ok := inline["operator_inputs"].([]any)
	if !ok {
		return nil
	}
	out := make([]server.InputSpec, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		spec := server.InputSpec{}
		spec.Path, _ = m["path"].(string)
		spec.Type, _ = m["type"].(string)
		if c, ok := m["constraints"].(map[string]any); ok {
			switch v := c["maxLength"].(type) {
			case int:
				spec.MaxLength = v
			case float64:
				spec.MaxLength = int(v)
			}
		}
		out = append(out, spec)
	}
	return out
}

func placeholderRole(placeholder string) (protocol.Role, bool) {
	switch placeholder {
	case "$TOKEN_OPERATOR":
		return protocol.RoleOperator, true
	case "$TOKEN_VIEWER":
		return protocol.RoleViewer, true
	case "$TOKEN_SERVICE":
		return protocol.RoleService, true
	case "$TOKEN_TEST":
		return protocol.RoleTest, true
	}
	return "", false
}

// errorCode maps a server.Scene error to a stable string code so the
// harness can recognise expected rejections.
func errorCode(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, server.ErrEmptyPatches) {
		return "EMPTY_PATCHES"
	}
	return "INVALID"
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

type problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail"`
}

func writeProblem(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(problem{
		Type:   "about:blank",
		Title:  fmt.Sprintf("control: %s", code),
		Status: status,
		Detail: detail,
	})
}
