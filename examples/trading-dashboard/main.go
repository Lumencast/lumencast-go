// Example trading-dashboard demonstrates a non-broadcast Lumencast
// scene : an orderbook + P&L view with bursty leaf-grain updates.
// Showcases that LSDP/1 is not specific to "broadcast graphics" —
// any leaf-grain reactive surface is in scope.
//
// Run :
//
//	go run ./examples/trading-dashboard
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
		ListenAddr: ":4001",
		Logger:     logger,
		Auth: server.NewStaticTokens(map[string]server.Identity{
			"trader":  {Subject: "trader-1", Role: protocol.RoleOperator},
			"watcher": {Subject: "watcher", Role: protocol.RoleViewer},
		}),
	})
	if err != nil {
		logger.Error("server: new", "err", err)
		os.Exit(1)
	}

	scene := srv.NewScene("trading", server.WithDeclaredInputs([]string{"__inputs.symbol"}))
	_ = scene.Set(map[string]any{
		"__inputs.symbol": "BTC-USD",
		"book.bid":        50000.0,
		"book.ask":        50001.0,
		"book.spread_bps": 2.0,
		"pnl.unrealized":  0.0,
		"pnl.realized":    0.0,
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go simulateBook(ctx, scene)

	logger.Info("trading-dashboard listening on :4001")
	if err := srv.Run(ctx); err != nil {
		logger.Error("server: run", "err", err)
		os.Exit(1)
	}
}

func simulateBook(ctx context.Context, scene *server.Scene) {
	t := time.NewTicker(50 * time.Millisecond) // 20 Hz
	defer t.Stop()
	rng := rand.New(rand.NewSource(time.Now().UnixNano())) //nolint:gosec
	bid := 50000.0
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			step := (rng.Float64() - 0.5) * 5
			bid += step
			ask := bid + 1
			spread := (ask - bid) / bid * 10000
			_ = scene.Emit(map[string]any{
				"book.bid":        round2(bid),
				"book.ask":        round2(ask),
				"book.spread_bps": round2(spread),
				"pnl.unrealized":  round2(rng.Float64()*100 - 50),
			})
		}
	}
}

func round2(v float64) float64 {
	return float64(int(v*100)) / 100
}
