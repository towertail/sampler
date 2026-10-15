# CLAUDE.md

Guidance for Claude Code in this repo.

## What this is

`towertail-sampler`: a Go binary that collects host metrics and writes JSON samples. The Towertail apps run it over SSH. Spec: [`docs/wire-format.md`](docs/wire-format.md).

## Commands

```bash
go build ./cmd/sampler
go test ./...
go vet ./...
gofmt -l .                 # must print nothing
scripts/build.sh           # all 7 triples
```

## Rules

- **Wire contract.** `pkg/schema` and `docs/wire-format.md` define the sample format. A breaking schema change bumps `v` and needs changes in the apps (`towertail/towertail`) and the server.
- **`--version` output** is `towertail-sampler <Version>+<SHA>`. The app bootstrap parses it. Do not change it.
- **Dependencies.** Use only the Go standard library, `gopsutil`, and `urfave/cli`. Ask before you add a dependency.
- **Prose.** Write docs, comments and commit messages in ASD-STE100 Simplified Technical English.
