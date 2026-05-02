# Benchmarks

> Status: TBD. Measured numbers land here once `v0.1.0` ships.

## Reference targets

The Go server kit aims for the budgets stated in [PERFORMANCE.md](https://github.com/Lumencast/lumencast-protocol/blob/main/spec/PERFORMANCE.md)
on a 4-core / 8 GiB Linux box :

| Metric | Target | Status |
|---|---|---|
| Concurrent WS subscribers (single scene) | 10 000 | TBD |
| Delta throughput (per scene) | 100 deltas/s | TBD |
| Boot to first WS accept | < 1 s | TBD |
| `lumencast` binary size (stripped) | < 20 MiB | TBD |

## Methodology (planned)

- `go test -bench=. ./protocol/...` — codec micro-benchmarks
- `examples/basic-scoreboard` driven by `lumencast conformance --client-cmd`
  with a synthetic load generator at 1 000 / 5 000 / 10 000 connections
- p50 / p95 / p99 reported, alongside heap and goroutine counts

## Running locally

```sh
go test -bench=. -benchmem ./protocol/...
```

Results published in this file with each tagged release.
