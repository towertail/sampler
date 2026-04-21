# sampler/

Single-binary Go collector that runs on each monitored server and emits one NDJSON sample per invocation (or per tick in streaming mode).

Shipped inside the Mac app bundle (five target triples) and pushed to `~/.towertail/sampler` on the remote host on first connect. See [`../docs/sampler.md`](../docs/sampler.md) for the spec, JSON schema, build matrix, and bootstrap handshake.

## Build

```
# from repo root
scripts/build.sampler.sh              # build all 5 targets → dist/samplers/<triple>/
scripts/build.sampler.sh darwin-arm64 # single target (fast iteration)
```

## Test

```
cd sampler && go test ./...
```

## Run locally

```
cd sampler
go run ./cmd/sampler --once          # emit one JSON sample
go run ./cmd/sampler --version       # print version
go run ./cmd/sampler --self-check    # print "ok" if the binary runs
go run ./cmd/sampler --interval 1s   # stream NDJSON once per second
```
