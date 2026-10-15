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

Push mode and the `service` command are experimental. Their flags and the server protocol can change in a minor release. Do not use them in production.

## Build from source

Go 1.24 or later is necessary.

```bash
scripts/build.sh                 # all 7 triples
go build ./cmd/sampler           # host platform only
go test ./...
```

## Release

GoReleaser ([`.goreleaser.yaml`](.goreleaser.yaml)) and [`.github/workflows/release.yml`](.github/workflows/release.yml) make the release.

1. Make sure that CI on `main` passes.
2. Tag the commit on `main` and push the tag:

   ```bash
   git tag v1.2.3
   git push origin v1.2.3
   ```

The workflow publishes a GitHub Release with these assets:

- 7 binaries, `towertail-sampler-<triple>` (`.exe` on Windows). The darwin binaries are signed with a Developer ID and notarized.
- `checksums.txt` (SHA-256 of the binaries).
- `manifest.json` (`version`, `sha` and the SHA-256 of each binary, after signing).
- `sample-v1.json` (the wire contract fixture).

A tag with a suffix (for example `v1.2.3-rc.1`) makes a pre-release.

The workflow uses the `release` environment. It needs these secrets:

| Secret | Value |
|---|---|
| `MACOS_SIGN_P12` | Base64 of the Developer ID Application `.p12` |
| `MACOS_SIGN_PASSWORD` | Password of the `.p12` |
| `MACOS_NOTARY_KEY` | Base64 of the App Store Connect API key (`.p8`) |
| `MACOS_NOTARY_KEY_ID` | Key ID of the API key |
| `MACOS_NOTARY_ISSUER_ID` | Issuer ID of the API key |

Without `MACOS_SIGN_P12`, the darwin binaries are not signed. The workflow then accepts only a pre-release tag.

To test the release locally (no signing, no upload):

```bash
goreleaser release --snapshot --clean
scripts/manifest.sh dist
```

## Wire format

The JSON schema, the flags, and the bootstrap handshake are in [`docs/wire-format.md`](docs/wire-format.md). The Go types are in [`pkg/schema`](pkg/schema).

Contract rule: [`testdata/sample-v1.json`](testdata/sample-v1.json) is the wire contract. A change to `pkg/schema` also updates the fixture. A breaking change bumps `SchemaVersion` and adds `testdata/sample-v<N>.json`.

## License

Apache-2.0. See [LICENSE](LICENSE).
