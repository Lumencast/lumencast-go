package protocol

import (
	"errors"
	"sync"
)

// SequenceTracker is a goroutine-safe per-subscription counter that
// implements the LSDP/1 sequencing rules :
//
//   - Snapshot starts a subscription with seq = 1.
//   - Each subsequent Delta / SceneChanged / Error increments by 1.
//   - SceneChanged is followed by a fresh Snapshot at seq = 1.
//
// On the receiver side, ObserveServer detects gaps (seq > last + 1)
// and returns ErrGap so the caller can close + reconnect with a
// VERSION_GAP reason. seq <= last is silently dropped (replay).
type SequenceTracker struct {
	mu  sync.Mutex
	cur uint64
}

// ErrGap signals a missing sequence number on the server-to-client
// stream. The receiver MUST close the WebSocket and reconnect ; a
// fresh Snapshot will reset state.
var ErrGap = errors.New("protocol: sequence gap")

// ErrInvalidSeqStart is returned by ObserveServer if the first frame
// of a subscription does not carry seq == 1.
var ErrInvalidSeqStart = errors.New("protocol: subscription must start at seq=1")

// NewSequenceTracker returns a tracker with cur = 0 (no frame seen).
func NewSequenceTracker() *SequenceTracker {
	return &SequenceTracker{}
}

// NextServer returns the next outgoing seq for a server emitting a
// frame on this subscription. Use Reset() when emitting the snapshot
// after a SceneChanged.
func (s *SequenceTracker) NextServer() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cur++
	return s.cur
}

// Reset rewinds the counter so the next NextServer call returns 1.
// Call this immediately before emitting the Snapshot that follows a
// SceneChanged.
func (s *SequenceTracker) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cur = 0
}

// Current returns the last seq emitted (0 if none yet).
func (s *SequenceTracker) Current() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur
}

// ObserveServer is the receiver-side check. Pass the seq from the
// most recently received server frame. Returns :
//
//   - nil : seq is the expected next value, advance.
//   - ErrGap : seq > last+1, caller MUST close and reconnect.
//   - nil with skip=true : seq <= last, replay — caller MUST drop frame.
//
// The tracker also accepts seq == 1 unconditionally (handles fresh
// Snapshot after SceneChanged or reconnect).
func (s *SequenceTracker) ObserveServer(seq uint64) (skip bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case seq == 1:
		// Fresh subscription / scene_changed — reset baseline.
		s.cur = 1
		return false, nil
	case s.cur == 0:
		// First non-1 frame on a tracker that never saw seq=1.
		return false, ErrInvalidSeqStart
	case seq == s.cur+1:
		s.cur = seq
		return false, nil
	case seq <= s.cur:
		return true, nil // silent replay
	default:
		return false, ErrGap
	}
}
