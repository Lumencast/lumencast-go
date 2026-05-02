# Contributing to lumencast-go

Thanks for your interest. This module is one of several reference SDKs for the
[Lumencast protocol](https://github.com/Lumencast/lumencast-protocol).

## Where to file what

| Subject | Repo |
|---|---|
| Wire-format question, LSDP/1 RFC, error code taxonomy | `Lumencast/lumencast-protocol` |
| Bug or improvement in `protocol/` codec | here |
| Bug or improvement in `server/` kit, CLI, examples | here |
| Cross-SDK consistency issues | `Lumencast/lumencast-protocol`, link the SDK PRs |

Spec-level governance — RFC process, decision log, conformance — lives in the
`lumencast-protocol` repo. Read [its CONTRIBUTING.md](https://github.com/Lumencast/lumencast-protocol/blob/main/CONTRIBUTING.md)
before proposing changes that affect on-wire behaviour.

## Local development

```sh
git clone --recurse-submodules https://github.com/Lumencast/lumencast-go
cd lumencast-go
go mod download
go test -race ./...
go test -tags=conformance ./...
```

If you forgot the recursive clone :

```sh
git submodule update --init --recursive
```

The `external/lumencast-protocol/` submodule pins the conformance fixtures to
a specific spec commit. Bumping it is its own PR.

## Code style

- `gofmt` / `goimports` — enforced in CI.
- `staticcheck ./...` — all checks pass.
- `golangci-lint run` — config in `.golangci.yml`, all enabled linters pass.
- Errors wrap with `%w` ; sentinel errors live near the package they originate
  in. No `panic` outside `init` and tests.
- Tests : `go test -race ./...` MUST stay green.

## Commit style

- Conventional title : `protocol:`, `server:`, `cli:`, `ci:`, `docs:`, `chore:`.
- Body explains **why**, not what (the diff is the what).
- Sign-off optional ; DCO not enforced for `v0`.

## PR checklist

- [ ] `go test -race ./...` green
- [ ] `go test -tags=conformance ./...` green
- [ ] `staticcheck ./...` clean
- [ ] `golangci-lint run` clean
- [ ] If the wire format changed : matching PR in `lumencast-protocol` linked
- [ ] If the public Go API changed : pkg.go.dev impact described in the PR

## Releases

Cut by maintainers only. Tag `vX.Y.Z` triggers `release.yml` ; `goreleaser`
publishes binaries. See `RELEASING.md` (TBD) for the runbook.

## Code of Conduct

By contributing, you agree to abide by [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).
