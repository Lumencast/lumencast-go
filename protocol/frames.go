package protocol

import "encoding/json"

// TransitionSpec is a per-leaf animation directive on a delta patch
// (LSDP/1.1 §3.2.2). Servers MAY emit it ; runtimes interpret it when
// applying the new value. 1.0 receivers ignore it.
//
// Kind is one of "tween", "spring", or "snap". Each kind reads a
// different subset of the remaining fields ; servers SHOULD only
// emit fields appropriate to the chosen kind.
type TransitionSpec struct {
	Kind       string  `json:"kind"`
	DurationMs int     `json:"duration_ms,omitempty"` // tween only
	Easing     string  `json:"easing,omitempty"`      // tween only — "linear", "ease-in", "ease-out", "ease-in-out"
	Stiffness  float64 `json:"stiffness,omitempty"`   // spring only
	Damping    float64 `json:"damping,omitempty"`     // spring only
}

// Cause is the optional provenance metadata on a delta (LSDP/1.1
// §3.2.3). Debug- and audit-only ; receivers MUST NOT use it for
// semantic decisions.
type Cause struct {
	Source  string `json:"source"`             // e.g. "operator:user-abc", "adapter:http_poll"
	InputID string `json:"input_id,omitempty"` // echoes Input.ClientMsgID
}

// SceneTransition is a show-level scene-swap transition on a
// scene_changed frame (LSDP/1.1 §3.3.1).
type SceneTransition struct {
	Kind       string `json:"kind"` // "crossfade" (more kinds reserved for future minors)
	DurationMs int    `json:"duration_ms,omitempty"`
}

// Snapshot is the full state of a subscription at a point in time.
// Server emits exactly one Snapshot per subscription, immediately
// after a successful Subscribe. Reconnection or SceneChanged
// triggers a new Snapshot with seq reset to 1.
//
// The State map MUST be flat : keys are dotted leaf paths, values
// are arbitrary JSON. Nested objects whose keys overlap with
// declared scene paths are forbidden.
type Snapshot struct {
	V            int                        `json:"v"`
	Type         string                     `json:"type"`
	Seq          uint64                     `json:"seq"`
	TS           string                     `json:"ts,omitempty"`
	SceneID      string                     `json:"scene_id"`
	SceneVersion string                     `json:"scene_version"`
	State        map[string]json.RawMessage `json:"state"`
}

// Delta carries one or more leaf patches. Patches are applied
// left-to-right ; the receiver MUST treat the whole delta as
// atomic — no partial application visible to the renderer.
//
// Patch.Value MUST NOT be a JSON object. Push leaf-grain patches
// for nested updates instead.
type Delta struct {
	V       int     `json:"v"`
	Type    string  `json:"type"`
	Seq     uint64  `json:"seq"`
	TS      string  `json:"ts,omitempty"`
	Patches []Patch `json:"patches"`

	// Cause is the optional provenance metadata for this delta
	// (LSDP/1.1 §3.2.3). Receivers MUST NOT use it for semantic
	// decisions — debug/audit only. 1.0 servers omit this field.
	Cause *Cause `json:"cause,omitempty"`
}

// Patch addresses a single leaf with its new value.
type Patch struct {
	Path  string          `json:"path"`
	Value json.RawMessage `json:"value"`

	// Transition is an optional per-leaf animation directive
	// (LSDP/1.1 §3.2.2). When present, the runtime applies the
	// described transition while interpolating to the new Value.
	// 1.0 receivers ignore. 1.0 servers omit.
	Transition *TransitionSpec `json:"transition,omitempty"`
}

// SceneChanged signals the active scene has been swapped. The
// runtime MUST refetch the bundle at SceneVersion and crossfade.
// The next server frame after a SceneChanged MUST be a Snapshot
// with seq = 1.
type SceneChanged struct {
	V            int    `json:"v"`
	Type         string `json:"type"`
	Seq          uint64 `json:"seq"`
	TS           string `json:"ts,omitempty"`
	SceneID      string `json:"scene_id"`
	SceneVersion string `json:"scene_version"`

	// FromSceneID is the previously active scene id (LSDP/1.1
	// §3.3.1). Lets the runtime render layered crossfades that need
	// both endpoints. 1.0 receivers ignore.
	FromSceneID string `json:"from_scene_id,omitempty"`

	// Transition is the show-level scene-swap directive (LSDP/1.1
	// §3.3.1). When absent, the runtime falls back to its default
	// crossfade. 1.0 receivers ignore.
	Transition *SceneTransition `json:"transition,omitempty"`
}

// Error is the server-emitted error frame. Recoverable=false signals
// "stop trying" ; the server MUST close the WebSocket within 1 second
// of sending. Recoverable=true keeps the connection open.
type Error struct {
	V           int    `json:"v"`
	Type        string `json:"type"`
	Seq         uint64 `json:"seq"`
	TS          string `json:"ts,omitempty"`
	Code        string `json:"code"`
	Message     string `json:"message"`
	Recoverable bool   `json:"recoverable"`

	// RetryAfterMs is optional and MAY be set on RATE_LIMIT errors.
	// Encoded as `retry_after_ms` ; runtimes MAY honour it as a
	// throttle hint.
	RetryAfterMs int `json:"retry_after_ms,omitempty"`
}

// Pong is the server's reply to a client Ping. Heartbeats carry no
// `seq` ; they are out-of-band per spec § 5.
type Pong struct {
	V    int    `json:"v"`
	Type string `json:"type"`

	// Nonce echoes the matching Ping.Nonce verbatim (LSDP/1.1 §3.5).
	// 1.0 servers reply with a bare pong.
	Nonce string `json:"nonce,omitempty"`
}

// Subscribe is the first frame a client sends after WebSocket open.
// Identifies the client (Token) and the subscription target.
//
//   - Token : opaque, server-validated.
//   - Scene : required for test mode, forbidden in live mode (server
//     picks the active scene). Encode/decode is permissive ; the
//     server enforces the conditional.
//   - Session : required for test mode with isolated session.
type Subscribe struct {
	V       int    `json:"v"`
	Type    string `json:"type"`
	Token   string `json:"token"`
	Scene   string `json:"scene,omitempty"`
	Session string `json:"session,omitempty"`

	// SinceSequence requests an incremental resume from a known
	// last-seen seq (LSDP/1.1 §4.1, §18). When the server's replay
	// buffer covers the gap, it responds with deltas resuming from
	// SinceSequence+1 ; otherwise it falls back to a fresh snapshot
	// (sequence reset). 1.0 servers MUST ignore this field. Zero
	// means "no resume requested" — start with a fresh snapshot.
	SinceSequence uint64 `json:"since_sequence,omitempty"`
}

// Input mutates operator inputs. Allowed for clients with operator,
// service or test role. Patches are atomic — if any patch is invalid,
// the server MUST reject the entire frame and apply nothing.
type Input struct {
	V       int     `json:"v"`
	Type    string  `json:"type"`
	Patches []Patch `json:"patches"`

	// ClientMsgID is a free-form correlation tag (LSDP/1.1 §4.2).
	// The server MUST echo this value verbatim in Cause.InputID of
	// the resulting delta — enables optimistic-UI reconciliation.
	// 1.0 servers ignore. Empty string means "no correlation".
	ClientMsgID string `json:"client_msg_id,omitempty"`
}

// Ping is a client-initiated heartbeat. Receiver MUST reply with Pong
// within 5 seconds.
type Ping struct {
	V    int    `json:"v"`
	Type string `json:"type"`

	// Nonce is a free-form correlation identifier (LSDP/1.1 §4.3).
	// The receiver MUST echo it verbatim in the Pong reply. 1.0
	// receivers reply with a bare Pong.
	Nonce string `json:"nonce,omitempty"`
}

// Unsubscribe is the clean-teardown signal a 1.1 client sends to
// indicate it is done with the subscription (LSDP/1.1 §4.4). The
// server MUST close the WebSocket within 1 second of receipt. No
// data flows after this frame. 1.0 servers receiving this MUST
// respond with an Error{Code: INTERNAL or UNKNOWN_TYPE} and close.
type Unsubscribe struct {
	V    int    `json:"v"`
	Type string `json:"type"`
}
