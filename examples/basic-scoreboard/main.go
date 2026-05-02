// Example basic-scoreboard demonstrates a minimal Lumencast server
// that publishes a two-player scoreboard with title and live scores.
//
// Run :
//
//	go run ./examples/basic-scoreboard
//
// Then connect a Lumencast runtime (e.g. @lumencast/runtime) to
// ws://localhost:4000/lsdp.v1 with subprotocol lsdp.v1 and the
// "demo-operator" or "demo-viewer" token.
package main

import (
	"context"
	"log/slog"
	"math/rand"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Lumencast/lumencast-go/protocol"
	"github.com/Lumencast/lumencast-go/server"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	srv, err := server.New(server.Config{
		ListenAddr: ":4000",
		Logger:     logger,
		Auth: server.NewStaticTokens(map[string]server.Identity{
			"demo-operator": {Subject: "alice", Role: protocol.RoleOperator},
			"demo-viewer":   {Subject: "guest", Role: protocol.RoleViewer},
		}),
	})
	if err != nil {
		logger.Error("server: new", "err", err)
		os.Exit(1)
	}

	scene := srv.NewScene("scoreboard",
		server.WithDeclaredInputs([]string{"__inputs.title"}),
	)
	_ = scene.Set(map[string]any{
		"__inputs.title":  "Match — Alice vs Bob",
		"players.0.name":  "Alice",
		"players.0.score": 0,
		"players.1.name":  "Bob",
		"players.1.score": 0,
		"period":          1,
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go simulateScores(ctx, scene)

	logger.Info("scoreboard listening on :4000")
	if err := srv.Run(ctx); err != nil {
		logger.Error("server: run", "err", err)
		os.Exit(1)
	}
}

// simulateScores nudges player scores at a 1 Hz cadence to drive
// runtime updates. In a real deployment this would come from an HTTP
// poller against your scoring endpoint.
func simulateScores(ctx context.Context, scene *server.Scene) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	rng := rand.New(rand.NewSource(time.Now().UnixNano())) //nolint:gosec
	scores := [2]int{0, 0}
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			i := rng.Intn(2)
			scores[i]++
			_ = scene.Emit(map[string]any{
				"players.0.score": scores[0],
				"players.1.score": scores[1],
			})
		}
	}
}
