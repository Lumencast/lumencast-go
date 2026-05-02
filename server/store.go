package server

import (
	"encoding/json"
	"fmt"
	"sync"
)

// store is the per-scene leaf-grain state map. It is an internal
// implementation detail of Scene ; the public surface is Scene.Set
// and Scene.Emit.
//
// Keys are dotted leaf paths ; values are arbitrary JSON. The store
// owns canonicalisation : a non-RawMessage value supplied to Set /
// Emit is marshalled once on entry so the snapshot path stays cheap.
type store struct {
	mu    sync.RWMutex
	state map[string]json.RawMessage
}

// newStore returns an empty store.
func newStore() *store {
	return &store{state: make(map[string]json.RawMessage)}
}

// snapshot returns a defensive copy of the entire state for inclusion
// in a Snapshot frame.
func (s *store) snapshot() map[string]json.RawMessage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cp := make(map[string]json.RawMessage, len(s.state))
	for k, v := range s.state {
		cp[k] = v
	}
	return cp
}

// applyPatches mutates the store and returns the canonicalised
// patches (encoded once) ready to ship in a Delta frame. Returns
// ErrEmptyPatches if patches is empty.
func (s *store) applyPatches(patches map[string]any) ([]rawPatch, error) {
	if len(patches) == 0 {
		return nil, ErrEmptyPatches
	}
	out := make([]rawPatch, 0, len(patches))
	s.mu.Lock()
	defer s.mu.Unlock()
	for path, value := range patches {
		raw, err := encodeValue(value)
		if err != nil {
			return nil, fmt.Errorf("encode %q: %w", path, err)
		}
		s.state[path] = raw
		out = append(out, rawPatch{Path: path, Value: raw})
	}
	return out, nil
}

// rawPatch is the canonicalised intermediate produced by applyPatches.
type rawPatch struct {
	Path  string
	Value json.RawMessage
}

// encodeValue normalises a user-supplied value to json.RawMessage.
// A json.RawMessage / []byte slice is passed through verbatim ;
// anything else is marshalled.
func encodeValue(v any) (json.RawMessage, error) {
	switch x := v.(type) {
	case nil:
		return json.RawMessage("null"), nil
	case json.RawMessage:
		// Validate it is in fact valid JSON.
		if !json.Valid(x) {
			return nil, fmt.Errorf("server: raw value is not valid JSON")
		}
		return x, nil
	case []byte:
		if !json.Valid(x) {
			return nil, fmt.Errorf("server: byte value is not valid JSON")
		}
		return json.RawMessage(x), nil
	default:
		return json.Marshal(v)
	}
}
