package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Lumencast/lumencast-go/protocol"
	"github.com/Lumencast/lumencast-go/server"
)

func cmdDev(args []string) int {
	fs := flag.NewFlagSet("dev", flag.ExitOnError)
	addr := fs.String("addr", ":4000", "listen address")
	verbose := fs.Bool("v", false, "verbose logging")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))

	srv, err := server.New(server.Config{
		ListenAddr: *addr,
		Logger:     logger,
		Auth: server.NewStaticTokens(map[string]server.Identity{
			"dev-operator": {Subject: "dev", Role: protocol.RoleOperator},
			"dev-viewer":   {Subject: "dev", Role: protocol.RoleViewer},
		}),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "lumencast dev: %v\n", err)
		return 1
	}

	scene := srv.NewScene("main-stage", server.WithDeclaredInputs([]string{"__inputs.title"}))
	_ = scene.Set(map[string]any{
		"__inputs.title": "lumencast dev",
		"counter":        0,
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		var n int
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				n++
				_ = scene.Emit(map[string]any{"counter": n})
			}
		}
	}()

	fmt.Printf("lumencast dev — listening on %s\n", *addr)
	fmt.Printf("  WebSocket: ws://localhost%s/lsdp.v1 (subprotocol: lsdp.v1)\n", *addr)
	fmt.Println("  Tokens: dev-operator | dev-viewer")
	fmt.Println("  Active scene: main-stage")
	fmt.Println("Press Ctrl-C to stop.")

	if err := srv.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "lumencast dev: %v\n", err)
		return 1
	}
	return 0
}
