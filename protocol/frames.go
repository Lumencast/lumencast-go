package protocol

import "encoding/json"

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
}

// Patch addresses a single leaf with its new value.
type Patch struct {
	Path  string          `json:"path"`
	Value json.RawMessage `json:"value"`
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
}

// Input mutates operator inputs. Allowed for clients with operator,
// service or test role. Patches are atomic — if any patch is invalid,
// the server MUST reject the entire frame and apply nothing.
type Input struct {
	V       int     `json:"v"`
	Type    string  `json:"type"`
	Patches []Patch `json:"patches"`
}

// Ping is a client-initiated heartbeat. Receiver MUST reply with Pong
// within 5 seconds.
type Ping struct {
	V    int    `json:"v"`
	Type string `json:"type"`
}
