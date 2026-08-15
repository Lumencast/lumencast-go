package server

import (
	"testing"

	"github.com/Lumencast/lumencast-go/protocol"
)

func TestSceneSubscribeWithResume_ReturnsReplayMetadata(t *testing.T) {
	scene := newScene("main")
	if err := scene.Set(map[string]any{"x": 1}); err != nil {
		t.Fatal(err)
	}
	md := &protocol.ProjectionMetadata{
		SchemaVersion:     "schema-v1",
		SceneDigest:       "digest-abc",
		RuntimeInstanceID: "runtime-xyz",
		Target:            "preview",
		RenderRevision:    "rev-123",
		CorrelationID:     "corr-456",
	}
	if err := scene.EmitWithCauseAndMetadata(map[string]any{"x": 2}, &protocol.Cause{Source: "svc"}, md); err != nil {
		t.Fatal(err)
	}

	// Since sequence 1 should be covered by buffer and replay the last delta.
	sub, snap, replay := scene.subscribeWithResume(64, true, true, 1)
	if sub == nil {
		t.Fatal("expected subscription")
	}
	if snap != nil {
		t.Fatalf("did not expect snapshot when replay is available")
	}
	if len(replay) != 1 {
		t.Fatalf("wanted 1 replay record, got %d", len(replay))
	}
	rec := replay[0]
	if rec.projectionMetadata == nil {
		t.Fatal("replay record metadata should be present")
	}
	if rec.projectionMetadata.SchemaVersion != "schema-v1" ||
		rec.projectionMetadata.SceneDigest != "digest-abc" ||
		rec.projectionMetadata.RuntimeInstanceID != "runtime-xyz" ||
		rec.projectionMetadata.Target != "preview" ||
		rec.projectionMetadata.RenderRevision != "rev-123" ||
		rec.projectionMetadata.CorrelationID != "corr-456" {
		t.Fatalf("unexpected replay metadata: %+v", rec.projectionMetadata)
	}
}

func TestSceneBackpressure_MarksSubscriptionStale(t *testing.T) {
	scene := newScene("main")
	if err := scene.Set(map[string]any{"x": 1}); err != nil {
		t.Fatal(err)
	}

	// One-slot buffer means the second emission is back-pressured quickly.
	sub, snap, records := scene.subscribeWithResume(1, true, true, 0)
	if sub == nil || snap == nil || records != nil {
		t.Fatal("unexpected subscribe state for first connection without replay")
	}

	if err := scene.Emit(map[string]any{"x": 2}); err != nil {
		t.Fatal(err)
	}
	if err := scene.Emit(map[string]any{"x": 3}); err != nil {
		t.Fatal(err)
	}

	if !sub.IsStale() {
		t.Fatal("expected subscription to be marked stale under backpressure")
	}
}
