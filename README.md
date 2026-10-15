# towertail-sampler

`towertail-sampler` is a small Go binary that collects host metrics (CPU, memory, swap, disks, network, processes, ports, and health signals). It writes one JSON sample per run, or one NDJSON line per tick.

The [Towertail](https://github.com/towertail/towertail) desktop apps upload this binary to `~/.towertail/` on each monitored host and run it over SSH. The binary has no runtime dependencies.

Supported targets (7 triples): `linux-amd64`, `linux-arm64`, `linux-armv7`, `darwin-amd64`, `darwin-arm64`, `windows-amd64`, `windows-arm64`.

## Install

1. Download the binary for your platform and `checksums.txt` from [GitHub Releases](https://github.com/towertail/sampler/releases).
2. Check the download:

   ```bash
   shasum -a 256 -c checksums.txt --ignore-missing
   ```

3. Make the binary executable and run it:

   ```bash
   chmod +x towertail-sampler
   ./towertail-sampler --version
   ```

## CLI modes

| Mode | Command | Result |
|---|---|---|
| once | `towertail-sampler once` or `--once` | Write one JSON sample and exit. This is the default. |
| stream | `towertail-sampler stream --interval 1s` or `--interval 1s` | Write one NDJSON line per tick until stdin closes. |
| self-check | `towertail-sampler self-check` or `--self-check` | Collect one sample, discard it, and print `ok`. |
| version | `towertail-sampler version`, `--version` or `-V` | Print `towertail-sampler <version>+<sha>`. |
| push (experimental) | `towertail-sampler push --endpoint <url> --token <token>` | Send samples over HTTPS to a Towertail server. |
| service (experimental) | `towertail-sampler service install ...` | Install push mode as a systemd unit or launchd agent. |

Use `--no-disk`, `--no-net`, `--no-proc`, `--no-ports` and `--no-health` to skip a collector. Run `towertail-sampler --help` for all flags.

The `once`, `stream` and `self-check` modes do not make network calls.

## Build from source

Go 1.24 or later is necessary.

```bash
scripts/build.sh                 # all 7 triples
go build ./cmd/sampler           # host platform only
go test ./...
```

## Wire format

The JSON schema, the flags, and the bootstrap handshake are in [`docs/wire-format.md`](docs/wire-format.md). The Go types are in [`pkg/schema`](pkg/schema).

## License

Apache-2.0. See [LICENSE](LICENSE).
