# lumencast-go

The canonical Go server SDK and `lumencast` CLI for [Lumencast](https://github.com/Lumencast/lumencast-protocol).

> **Status**: pre-`v0.1.0`. The wire protocol (LSDP/1) and scene format
> (LSML 1.0) are draft and will be frozen on first conformance-pass-everywhere
> release.

This module ships :

- **`protocol/`** — pure LSDP/1 codec : envelopes, frames, sequencing, leaf
  paths, error taxonomy. Zero transport coupling.
- **`server/`** — server kit : WebSocket subscription handler, scene store,
  role enforcement, token-agnostic auth hook, adapter helpers (HTTP poll,
  WS subscribe, Postgres LISTEN).
- **`conformance/`** — harness for the
  [LSDP/1 conformance suite](https://github.com/Lumencast/lumencast-protocol/tree/main/conformance).
- **`cmd/lumencast/`** — single-binary CLI : `init`, `dev`, `validate`,
  `conformance`, `build`, `serve`.

## Why Go is canonical

Lumencast is **protocol-first**. Multiple SDKs implement LSDP/1 — `lumencast-js`,
`lumencast-py`, `lumencast-rs`, `lumencast-go`. When a behaviour is ambiguous
across implementations, **the Go SDK is the tiebreaker**. Wire-level concerns
(HTTP, WebSocket, concurrency, single-binary distribution) match Go's
strengths idiomatically.

## Quick start — server kit

```go
package main

import (
    "context"
    "time"

    "github.com/Lumencast/lumencast-go/server"
)

func main() {
    srv := server.New(server.Config{
        ListenAddr: ":4000",
        Auth:       server.StaticTokens(map[string]server.Identity{
            "operator-token": {Subject: "alice", Role: server.RoleOperator},
            "viewer-token":   {Subject: "guest", Role: server.RoleViewer},
        }),
    })

    scene := srv.NewScene("main-stage")
    scene.Set(map[string]any{
        "show.title":        "Hello",
        "players.0.name":    "Alice",
        "players.0.score":   0,
        "players.1.name":    "Bob",
        "players.1.score":   0,
    })

    ctx := context.Background()
    go func() {
        ticker := time.NewTicker(200 * time.Millisecond)
        defer ticker.Stop()
        for {
            select {
            case <-ticker.C:
                scene.Emit(map[string]any{
                    "players.0.score": time.Now().Unix() % 100,
                })
            case <-ctx.Done():
                return
            }
        }
    }()

    _ = srv.Run(ctx)
}
```

## Quick start — CLI

```sh
go install github.com/Lumencast/lumencast-go/cmd/lumencast@latest

lumencast init demo
cd demo
lumencast dev                    # mock server + local runtime preview
lumencast validate scene.json    # schema validate an LSML bundle
lumencast conformance --server ws://localhost:4000/lsdp.v1
```

Pre-built binaries are published on the
[GitHub Releases](https://github.com/Lumencast/lumencast-go/releases) page
for `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`,
`windows/amd64`.

## Package matrix

| Path | Purpose | Stability |
|---|---|---|
| `protocol/` | LSDP/1 codec, types, error codes | tied to LSDP/1 |
| `server/` | Server kit (WS handler, scene store, auth, adapters) | `v0.x` — API may evolve |
| `server/adapters/` | HTTP poll, WebSocket subscribe, Postgres LISTEN/NOTIFY (pgx) | `v0.x` |
| `lsml/` | LSML 1.0 bundle types, validator, content-hash canonicalisation | `v0.x` |
| `conformance/` | Conformance harness with embedded fixtures and `$BUNDLE.id.hash` placeholder support | `v0.x` |
| `cmd/lumencast/` | CLI binary | `v0.x` |

## Go version

Minimum supported Go version : **1.25**. The pgx LISTEN/NOTIFY adapter
pulls `github.com/jackc/pgx/v5`, which sets the floor.

## Conformance

```sh
go test -tags=conformance ./...
```

Runs the embedded conformance suite (vendored from
`lumencast-protocol/conformance/v1/`). All `required` scenarios MUST pass.

## Performance

The Go server SHOULD :

- Handle 10 000 concurrent WS subscribers on a single 4-core / 8 GiB machine
- Emit 100 deltas/second/scene without backpressure
- Boot to first WS accept in less than 1 s

These are non-normative for `v0`. See `BENCHMARKS.md` for measured numbers.

## Documentation

- [LSDP/1 wire spec](https://github.com/Lumencast/lumencast-protocol/blob/main/spec/LSDP-1.md)
- [LSML 1.0 scene format](https://github.com/Lumencast/lumencast-protocol/blob/main/spec/LSML-1.md)
- [Error code taxonomy](https://github.com/Lumencast/lumencast-protocol/blob/main/spec/ERROR-CODES.md)
- [Runtime API contract](https://github.com/Lumencast/lumencast-protocol/blob/main/spec/RUNTIME-API.md)
- [Performance budgets](https://github.com/Lumencast/lumencast-protocol/blob/main/spec/PERFORMANCE.md)

## License

Apache 2.0 — see [LICENSE](LICENSE).
