package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestEncodeServerFrames(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want string
	}{
		{
			name: "snapshot",
			in: Snapshot{
				Seq:          1,
				SceneID:      "main-stage",
				SceneVersion: "sha256:abc",
				State: map[string]json.RawMessage{
					"show.title": json.RawMessage(`"Hello"`),
				},
			},
			want: `{"v":1,"type":"snapshot","seq":1,"scene_id":"main-stage","scene_version":"sha256:abc","state":{"show.title":"Hello"}}`,
		},
		{
			name: "delta",
			in: Delta{
				Seq: 42,
				Patches: []Patch{
					{Path: "players.0.score", Value: json.RawMessage(`7`)},
				},
			},
			want: `{"v":1,"type":"delta","seq":42,"patches":[{"path":"players.0.score","value":7}]}`,
		},
		{
			name: "scene_changed",
			in: SceneChanged{
				Seq:          100,
				SceneID:      "intermission",
				SceneVersion: "sha256:def",
			},
			want: `{"v":1,"type":"scene_changed","seq":100,"scene_id":"intermission","scene_version":"sha256:def"}`,
		},
		{
			name: "error",
			in: Error{
				Seq:         50,
				Code:        string(CodeWriteForbidden),
				Message:     "viewer cannot send input",
				Recoverable: false,
			},
			want: `{"v":1,"type":"error","seq":50,"code":"WRITE_FORBIDDEN","message":"viewer cannot send input","recoverable":false}`,
		},
		{
			name: "pong",
			in:   Pong{},
			want: `{"v":1,"type":"pong"}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Encode(tc.in)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			if string(got) != tc.want {
				t.Fatalf("\nwant: %s\n got: %s", tc.want, got)
			}
		})
	}
}

func TestEncodeStampsEnvelope(t *testing.T) {
	// Caller leaves V/Type zero — Encode MUST stamp them.
	raw, err := Encode(Delta{Seq: 1, Patches: []Patch{{Path: "x", Value: json.RawMessage(`1`)}}})
	if err != nil {
		t.Fatal(err)
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	if env.V != Version || env.Type != TypeDelta {
		t.Fatalf("envelope not stamped: %+v", env)
	}
}

func TestDecodeClientFrames(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want any
	}{
		{
			name: "subscribe live",
			raw:  `{"v":1,"type":"subscribe","token":"tok"}`,
			want: &Subscribe{V: 1, Type: TypeSubscribe, Token: "tok"},
		},
		{
			name: "subscribe test",
			raw:  `{"v":1,"type":"subscribe","token":"tok","scene":"s","session":"sess"}`,
			want: &Subscribe{V: 1, Type: TypeSubscribe, Token: "tok", Scene: "s", Session: "sess"},
		},
		{
			name: "input",
			raw:  `{"v":1,"type":"input","patches":[{"path":"__inputs.title","value":"hi"}]}`,
			want: &Input{V: 1, Type: TypeInput, Patches: []Patch{{Path: "__inputs.title", Value: json.RawMessage(`"hi"`)}}},
		},
		{
			name: "ping",
			raw:  `{"v":1,"type":"ping"}`,
			want: &Ping{V: 1, Type: TypePing},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Decode([]byte(tc.raw))
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(tc.want)
			if !bytes.Equal(gotJSON, wantJSON) {
				t.Fatalf("\nwant: %s\n got: %s", wantJSON, gotJSON)
			}
		})
	}
}

func TestDecodeRejectsBadVersion(t *testing.T) {
	_, err := Decode([]byte(`{"v":2,"type":"subscribe","token":"x"}`))
	if !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("want ErrVersionMismatch, got %v", err)
	}
}

func TestDecodeRejectsUnknownType(t *testing.T) {
	_, err := Decode([]byte(`{"v":1,"type":"banana"}`))
	if !errors.Is(err, ErrUnknownType) {
		t.Fatalf("want ErrUnknownType, got %v", err)
	}
}

func TestDecodeServerFrames(t *testing.T) {
	raw := `{"v":1,"type":"snapshot","seq":1,"scene_id":"x","scene_version":"sha256:y","state":{}}`
	msg, err := DecodeServer([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	snap, ok := msg.(*Snapshot)
	if !ok {
		t.Fatalf("got %T, want *Snapshot", msg)
	}
	if snap.SceneID != "x" || snap.Seq != 1 {
		t.Fatalf("unexpected: %+v", snap)
	}
}

func TestRoundTripPreservesRawValues(t *testing.T) {
	// Ensures we don't lose fidelity on null vs missing, integer vs
	// float, etc.
	original := `{"v":1,"type":"delta","seq":2,"patches":[{"path":"a","value":null},{"path":"b","value":3.14},{"path":"c","value":[1,2,3]}]}`
	msg, err := DecodeServer([]byte(original))
	if err != nil {
		t.Fatal(err)
	}
	d := msg.(*Delta)
	out, err := Encode(d)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != original {
		t.Fatalf("\nwant: %s\n got: %s", original, out)
	}
}

func TestEncodeNoHTMLEscape(t *testing.T) {
	// Paths with `<`, `>`, `&` MUST survive on the wire.
	d := Delta{Seq: 1, Patches: []Patch{{Path: "a<b>&c", Value: json.RawMessage(`"x"`)}}}
	out, err := Encode(d)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "a<b>&c") {
		t.Fatalf("HTML chars escaped: %s", out)
	}
}

func TestSequenceTracker_ServerEmit(t *testing.T) {
	s := NewSequenceTracker()
	if got := s.NextServer(); got != 1 {
		t.Fatalf("first NextServer = %d, want 1", got)
	}
	if got := s.NextServer(); got != 2 {
		t.Fatalf("second NextServer = %d, want 2", got)
	}
	s.Reset()
	if got := s.NextServer(); got != 1 {
		t.Fatalf("after reset NextServer = %d, want 1", got)
	}
}

func TestSequenceTracker_ObserveServer(t *testing.T) {
	s := NewSequenceTracker()

	// First frame must be seq=1.
	if skip, err := s.ObserveServer(1); err != nil || skip {
		t.Fatalf("seq=1 first: skip=%v err=%v", skip, err)
	}
	// Contiguous.
	if skip, err := s.ObserveServer(2); err != nil || skip {
		t.Fatalf("seq=2: skip=%v err=%v", skip, err)
	}
	// Replay.
	if skip, err := s.ObserveServer(2); err != nil || !skip {
		t.Fatalf("replay seq=2: skip=%v err=%v", skip, err)
	}
	// Gap.
	if _, err := s.ObserveServer(5); !errors.Is(err, ErrGap) {
		t.Fatalf("seq=5: want ErrGap, got %v", err)
	}
}

func TestSequenceTracker_RejectsInvalidStart(t *testing.T) {
	s := NewSequenceTracker()
	if _, err := s.ObserveServer(2); !errors.Is(err, ErrInvalidSeqStart) {
		t.Fatalf("first non-1: want ErrInvalidSeqStart, got %v", err)
	}
}

func TestSequenceTracker_SceneChangedReset(t *testing.T) {
	s := NewSequenceTracker()
	_, _ = s.ObserveServer(1)
	_, _ = s.ObserveServer(2)
	_, _ = s.ObserveServer(3)
	// Scene changed → next snapshot resets to 1.
	if skip, err := s.ObserveServer(1); err != nil || skip {
		t.Fatalf("seq=1 after change: skip=%v err=%v", skip, err)
	}
}

func TestLeafPath_Validate(t *testing.T) {
	good := []string{
		"a",
		"a.b.c",
		"players.0.name",
		"__inputs.title",
		"__inputs.platform.twitch.last_chat",
	}
	for _, p := range good {
		if err := LeafPath(p).Validate(); err != nil {
			t.Errorf("%q: unexpected error %v", p, err)
		}
	}
	bad := []string{
		"",
		".a",
		"a.",
		"a..b",
		"a-b",
		"a.b c",
		"{x}.y", // template form rejected by Validate
	}
	for _, p := range bad {
		if err := LeafPath(p).Validate(); err == nil {
			t.Errorf("%q: expected error", p)
		}
	}
}

func TestLeafPath_ValidateTemplate(t *testing.T) {
	if err := LeafPath("{player}.score").ValidateTemplate(); err != nil {
		t.Fatalf("template: %v", err)
	}
	if err := LeafPath("{}").ValidateTemplate(); err == nil {
		t.Fatal("empty scope should fail")
	}
	if err := LeafPath("{x-y}").ValidateTemplate(); err == nil {
		t.Fatal("invalid scope should fail")
	}
}

func TestLeafPath_Substitute(t *testing.T) {
	got, err := LeafPath("{player}.score").Substitute(map[string]string{"player": "players.0"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "players.0.score" {
		t.Fatalf("got %q", got)
	}

	if _, err := LeafPath("{missing}.x").Substitute(map[string]string{}); err == nil {
		t.Fatal("missing scope should fail")
	}
	if _, err := LeafPath("{a.score").Substitute(map[string]string{}); err == nil {
		t.Fatal("unterminated should fail")
	}
}

func TestLeafPath_IsReserved(t *testing.T) {
	cases := map[string]bool{
		"__inputs":           true,
		"__inputs.title":     true,
		"__system.adapter.x": true,
		"__test.input":       true,
		"__schema.x":         true,
		"foo":                false,
		"_partial":           false,
		"__custom":           false, // not in the reserved set
	}
	for p, want := range cases {
		if got := LeafPath(p).IsReserved(); got != want {
			t.Errorf("%q: got %v want %v", p, got, want)
		}
	}
}

func TestLeafPath_HasPrefix(t *testing.T) {
	p := LeafPath("players.0.score")
	if !p.HasPrefix("players") {
		t.Fail()
	}
	if !p.HasPrefix("players.0") {
		t.Fail()
	}
	if p.HasPrefix("playerstats") {
		t.Fail()
	}
}

func TestErrorCode_Recoverable(t *testing.T) {
	cases := map[ErrorCode]bool{
		CodeWriteForbidden:     true,
		CodeBundleFetchFailed:  true,
		CodeVersionGap:         true,
		CodeUnknownPath:        true,
		CodeInvalidValue:       true,
		CodeRateLimit:          true,
		CodeAuthDenied:         false,
		CodeSceneNotFound:      false,
		CodeBundleIncompatible: false,
		CodeVersionMismatch:    false,
		CodeTestSessionExpired: false,
	}
	for code, want := range cases {
		if got := code.Recoverable(false); got != want {
			t.Errorf("%s: got %v want %v", code, got, want)
		}
	}
	// CodeInternal honours the supplied default.
	if !CodeInternal.Recoverable(true) {
		t.Fatal("internal recoverable=true not honoured")
	}
	if CodeInternal.Recoverable(false) {
		t.Fatal("internal recoverable=false not honoured")
	}
}

func TestRole_Validity(t *testing.T) {
	if !RoleOperator.IsValid() || !RoleViewer.IsValid() ||
		!RoleService.IsValid() || !RoleTest.IsValid() {
		t.Fatal("known roles must be valid")
	}
	if Role("admin").IsValid() {
		t.Fatal("unknown role must not be valid")
	}
}
