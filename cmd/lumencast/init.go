package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func cmdInit(args []string) int {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	dir := fs.String("dir", "", "target directory (default: project name)")
	module := fs.String("module", "", "Go module path (default: github.com/you/<name>)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "lumencast init: project name required")
		return 2
	}
	name := fs.Arg(0)
	target := *dir
	if target == "" {
		target = name
	}
	mod := *module
	if mod == "" {
		mod = "github.com/you/" + name
	}

	if _, err := os.Stat(target); err == nil {
		fmt.Fprintf(os.Stderr, "lumencast init: %s already exists — refusing to overwrite\n", target)
		return 1
	}

	files := scaffoldFiles(name, mod)
	for path, body := range files {
		full := filepath.Join(target, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return diag(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			return diag(err)
		}
	}

	fmt.Printf("Scaffolded %s in %s\n", name, target)
	fmt.Println("Next :")
	fmt.Printf("  cd %s\n", target)
	fmt.Println("  go mod tidy")
	fmt.Println("  lumencast dev")
	return 0
}

func diag(err error) int {
	fmt.Fprintf(os.Stderr, "lumencast init: %v\n", err)
	return 1
}

func scaffoldFiles(name, module string) map[string]string {
	return map[string]string{
		"go.mod": fmt.Sprintf(`module %s

go 1.22

require github.com/Lumencast/lumencast-go v0.1.0
`, module),

		"main.go": fmt.Sprintf(`package main

import (
	"context"
	"log/slog"
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
		// TODO: replace StaticTokens with your real authenticator
		// before deploying. Static tokens are for local dev only.
		Auth: server.NewStaticTokens(map[string]server.Identity{
			"dev-operator": {Subject: "dev", Role: protocol.RoleOperator},
			"dev-viewer":   {Subject: "dev", Role: protocol.RoleViewer},
		}),
	})
	if err != nil {
		logger.Error("server: new", "err", err)
		os.Exit(1)
	}

	scene := srv.NewScene("main-stage",
		server.WithDeclaredInputs([]string{"__inputs.title"}),
	)
	_ = scene.Set(map[string]any{
		"__inputs.title": "Hello %s",
		"counter":        0,
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go tickCounter(ctx, scene)

	logger.Info("server starting", "addr", ":4000")
	if err := srv.Run(ctx); err != nil {
		logger.Error("server: run", "err", err)
		os.Exit(1)
	}
}

func tickCounter(ctx context.Context, scene *server.Scene) {
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
}
`, name),

		"scene.json": `{
  "lsml": "1.0",
  "scene_id": "main-stage",
  "scene_version": "sha256:0000000000000000000000000000000000000000000000000000000000000000",
  "layout": {
    "kind": "stack",
    "direction": "vertical",
    "gap": 12,
    "children": [
      { "kind": "text", "bind": { "value": "__inputs.title" }, "style": { "fontSize": 48 } },
      { "kind": "text", "bind": { "value": "counter" }, "style": { "fontSize": 24 } }
    ]
  },
  "operator_inputs": [
    {
      "path": "__inputs.title",
      "label": "Title",
      "type": "string",
      "writable_by": ["operator"]
    }
  ],
  "defaults": {
    "__inputs.title": "Hello",
    "counter": 0
  }
}
`,

		"README.md": fmt.Sprintf(`# %s

A Lumencast project scaffolded with `+"`lumencast init`"+`.

## Run

`+"```sh"+`
go mod tidy
go run .
`+"```"+`

The server listens on :4000. Connect a runtime via WebSocket :

`+"```"+`
ws://localhost:4000/lsdp.v1
Sec-WebSocket-Protocol: lsdp.v1
`+"```"+`

## Security

The default authenticator is `+"`server.StaticTokens`"+`. **Replace it before
deploying.** See `+"`SECURITY.md`"+` in the lumencast-go repo for guidance.
`, name),

		".gitignore": `/dist/
/bin/
*.exe
*.test
*.out
`,
	}
}
