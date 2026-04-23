# sampler/docker

Run the Towertail sampler as a container. Pairs with the server stack in [`../../server/docker`](../../server/docker).

## Usage

```bash
export TOWERTAIL_ENDPOINT=https://towertail.example.com
export TOWERTAIL_TOKEN=tt_...                 # issued by the server
docker compose up -d
```

## Why host namespaces?

On Linux `gopsutil` reads `/proc`, `/sys`, and the network interface list directly. Without `pid: host` and `network_mode: host`, the sampler only sees the container's own view — which is useless for host monitoring. The bind mount at `/hostfs` plus `HOST_PROC=/hostfs/proc` gives gopsutil access to the host's procfs and sysfs.

## Build locally

```bash
cd ..
docker build -t towertail-sampler:local -f docker/Dockerfile .
```

## Alternative: native install

If Docker isn't desired, use `towertail-sampler service install` to register the binary as a systemd unit or launchd agent — see [`../README.md`](../README.md).
