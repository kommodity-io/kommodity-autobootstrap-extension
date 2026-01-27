# Talos Auto-Bootstrap Extension

[![Go Report Card](https://img.shields.io/badge/go%20report-A+-brightgreen?style=flat-square)](https://goreportcard.com/report/github.com/kommodity-io/kommodity-autobootstrap-extension)
[![Go Reference](https://img.shields.io/badge/godoc-reference-blue?style=flat-square)](https://pkg.go.dev/github.com/kommodity-io/kommodity-autobootstrap-extension)
[![CI](https://img.shields.io/github/actions/workflow/status/kommodity-io/kommodity-autobootstrap-extension/release.yml?branch=main&label=ci&style=flat-square)](https://github.com/kommodity-io/kommodity-autobootstrap-extension/actions)
[![Release](https://img.shields.io/github/v/release/kommodity-io/kommodity-autobootstrap-extension?include_prereleases&label=release&style=flat-square)](https://github.com/kommodity-io/kommodity-autobootstrap-extension/releases)
[![License](https://img.shields.io/github/license/kommodity-io/kommodity-autobootstrap-extension?style=flat-square)](https://github.com/kommodity-io/kommodity-autobootstrap-extension/blob/main/LICENSE)

A Talos Linux system extension that automatically bootstraps Kubernetes clusters without manual intervention.

> **EXPERIMENTAL**: This project is in an early stage of development and is not yet ready for production use. APIs may break between minor releases. The project does however adhere to [semantic versioning](https://semver.org), so patch releases will never break the API.

## How It Works

The extension runs as a Talos system extension service on control plane nodes. It:

1. **Detects control plane nodes** by checking for the presence of `/system/secrets/etcd`
2. **Generates admin credentials** by reading the machine CA from the STATE partition
3. **Connects to the local Talos API** (apid) on port 50000
4. **Discovers peer nodes** by scanning the local network CIDR
5. **Elects a leader** deterministically based on boot time
6. **Bootstraps the cluster** when quorum is reached

## Architecture

```
┌──────────────────────────────────────────────────────────────────────────┐
│                         TALOS AUTO-BOOTSTRAP                             │
│                                                                          │
│  ┌────────────────────────────────────────────────────────────────────┐  │
│  │                         STARTUP PHASE                              │  │
│  │                                                                    │  │
│  │  1. Check /system/secrets/etcd (control plane detection)           │  │
│  │  2. Exit if worker node (etcd secrets don't exist)                 │  │
│  │  3. Get network info (local IP, CIDR, gateway)                     │  │
│  │  4. Read machine CA from STATE partition (/dev/disk/by-partlabel)  │  │
│  │  5. Generate admin TLS credentials from machine CA                 │  │
│  │  6. Connect to local apid on port 50000                            │  │
│  └────────────────────────────────────────────────────────────────────┘  │
│                                    │                                     │
│                                    ▼                                     │
│  ┌────────────────────────────────────────────────────────────────────┐  │
│  │                      MAIN BOOTSTRAP LOOP                           │  │
│  │                                                                    │  │
│  │  ┌──────────────┐    ┌──────────────┐    ┌──────────────────────┐  │  │
│  │  │   Bootstrap  │    │    CIDR      │    │   Node Detection     │  │  │
│  │  │    Check     │───▶│   Scanner    │───▶│   & Validation       │  │  │
│  │  │              │    │              │    │                      │  │  │
│  │  │ etcd members │    │ Probe :50000 │    │ Get machine type     │  │  │
│  │  │ (5s timeout) │    │ on each IP   │    │ Get boot time        │  │  │
│  │  └──────────────┘    └──────────────┘    └──────────────────────┘  │  │
│  │                                                   │                │  │
│  │                                                   ▼                │  │
│  │  ┌────────────────────────────────────────────────────────────────┐│  │
│  │  │                    LEADER ELECTION                             ││  │
│  │  │                                                                ││  │
│  │  │  1. Collect all control plane nodes (including self)           ││  │
│  │  │  2. Wait for quorum (configurable number of nodes)             ││  │
│  │  │  3. Sort by boot time (ascending)                              ││  │
│  │  │  4. Tie-break by IP address (lowest wins)                      ││  │
│  │  │  5. First node in sorted list = LEADER                         ││  │
│  │  └────────────────────────────────────────────────────────────────┘│  │
│  └────────────────────────────────────────────────────────────────────┘  │
│                                    │                                     │
│                    ┌───────────────┴───────────────┐                     │
│                    ▼                               ▼                     │
│  ┌─────────────────────────────┐  ┌─────────────────────────────────┐    │
│  │      LEADER PATH            │  │       FOLLOWER PATH             │    │
│  │                             │  │                                 │    │
│  │ 1. Pre-bootstrap delay      │  │ 1. Wait for check interval      │    │
│  │ 2. Check not bootstrapped   │  │ 2. Check if bootstrapped        │    │
│  │ 3. Execute Bootstrap()      │  │ 3. If yes: exit success         │    │
│  │ 4. Wait for etcd ready      │  │ 4. If no: re-run discovery      │    │
│  │ 5. Exit success             │  │                                 │    │
│  └─────────────────────────────┘  └─────────────────────────────────┘    │
└──────────────────────────────────────────────────────────────────────────┘
```

## Features

### Automatic Bootstrap

Eliminates the need for operators to manually run `talosctl bootstrap`. The extension automatically discovers peers, elects a leader, and bootstraps the cluster when quorum is reached.

### Self-Contained Credential Generation

The extension generates its own admin credentials by:
- Mounting the STATE partition read-only
- Reading the machine CA certificate and key from the machine config
- Generating a short-lived (24h) client certificate with `os:admin` role
- Using these credentials to authenticate with the local Talos API

This means the extension works without any external secrets or pre-configured credentials.

### Peer Discovery

Discovers peer Talos nodes via CIDR network scanning:
- Reads network configuration from `/proc/net/route` and network interfaces
- Scans the local CIDR range for other Talos nodes on port 50000
- Uses insecure TLS for discovery (required for unknown nodes)
- Identifies control plane vs worker nodes via machine type
- Retrieves boot time for leader election

### Deterministic Leader Election

Implements a deterministic leader election algorithm:

1. Collect all control plane nodes (including self)
2. Wait until quorum is reached (configurable)
3. Sort by boot time (oldest first)
4. Tie-break by IP address (lowest wins)
5. First node in sorted list becomes leader

This ensures the same leader is elected given the same conditions, preventing race conditions.

### Safe Bootstrap Coordination

The leader performs multiple safety checks before bootstrapping:

- Pre-bootstrap delay to allow late-joining nodes to participate
- Final verification that cluster hasn't already been bootstrapped
- Waits for etcd to become ready after bootstrap

### Fault Tolerance

- Retries on transient failures with exponential backoff
- Configurable maximum backoff duration
- Graceful handling of worker nodes (exits cleanly)
- Continues operation if some peers are unreachable
- Short timeout (5s) on bootstrap status checks to prevent hanging

## Configuration

The extension is configured via `ExtensionServiceConfig` in the Talos machine config. Environment variables are passed through the `environment` field:

| Environment Variable | Description | Default |
|---|---|---|
| `LOG_LEVEL` | Logging verbosity: debug, info, warn, error | `info` |
| `TALOS_AUTO_BOOTSTRAP_SCAN_INTERVAL` | Interval between network discovery scans | `30s` |
| `TALOS_AUTO_BOOTSTRAP_FOLLOWER_CHECK_INTERVAL` | How often followers check bootstrap status | `15s` |
| `TALOS_AUTO_BOOTSTRAP_QUORUM_NODES` | Number of control plane nodes required before bootstrapping | `1` |
| `TALOS_AUTO_BOOTSTRAP_PRE_BOOTSTRAP_DELAY` | Leader wait time before executing bootstrap | `10s` |
| `TALOS_AUTO_BOOTSTRAP_MAX_BACKOFF` | Maximum retry backoff duration | `2m` |
| `TALOS_AUTO_BOOTSTRAP_SCAN_TIMEOUT` | Timeout for probing each node during discovery | `2s` |
| `TALOS_AUTO_BOOTSTRAP_SCAN_CONCURRENCY` | Maximum concurrent node probes | `50` |

## Deployment

### Building a Talos Image with the Extension

The extension must be included in your Talos image. Use the Talos imager:

```shell
# Using docker
docker run --rm -t -v $PWD/_out:/out ghcr.io/siderolabs/imager:v1.11.3 \
  --system-extension-image ghcr.io/kommodity-io/kommodity-autobootstrap-extension:latest \
  metal
```

Or with `talosctl`:

```shell
talosctl image default \
  --image-extension ghcr.io/kommodity-io/kommodity-autobootstrap-extension:latest
```

### Configuration via Strategic Patches

Configure the extension using Talos strategic patches (works with CAPI and standalone):

```yaml
apiVersion: v1alpha1
kind: ExtensionServiceConfig
name: kommodity-autobootstrap
environment:
  - LOG_LEVEL=info
  - TALOS_AUTO_BOOTSTRAP_SCAN_INTERVAL=30s
  - TALOS_AUTO_BOOTSTRAP_QUORUM_NODES=3
  - TALOS_AUTO_BOOTSTRAP_PRE_BOOTSTRAP_DELAY=15s
  - TALOS_AUTO_BOOTSTRAP_MAX_BACKOFF=2m
  - TALOS_AUTO_BOOTSTRAP_SCAN_TIMEOUT=2s
```

### Example: 3-Node HA Control Plane

For a 3-node HA control plane cluster:

```yaml
apiVersion: v1alpha1
kind: ExtensionServiceConfig
name: kommodity-autobootstrap
environment:
  - LOG_LEVEL=info
  - TALOS_AUTO_BOOTSTRAP_QUORUM_NODES=3
  - TALOS_AUTO_BOOTSTRAP_PRE_BOOTSTRAP_DELAY=20s
```

This ensures:
- All 3 nodes must be discovered before bootstrap
- 20 second delay gives time for late-joining nodes
- Leader election picks the oldest node

### Example: Single-Node Cluster

For development or single-node clusters:

```yaml
apiVersion: v1alpha1
kind: ExtensionServiceConfig
name: kommodity-autobootstrap
environment:
  - LOG_LEVEL=info
  - TALOS_AUTO_BOOTSTRAP_QUORUM_NODES=1
  - TALOS_AUTO_BOOTSTRAP_PRE_BOOTSTRAP_DELAY=5s
```

## Service Definition

The extension runs as a Talos extension service with the following characteristics:

- **Depends on**: `apid` service and network connectivity
- **Restart policy**: `untilSuccess` (keeps trying until bootstrap succeeds)
- **Mounts**:
  - `/system/secrets` (read-only) - for control plane detection
  - `/proc` as `/host/proc` (read-only) - for boot time and network routes
  - `/etc` (read-only) - for hostname
  - `/dev` (read-only) - for STATE partition access
  - `/run` (read-write) - for temporary mount points

## Development

### Prerequisites

- Go 1.24+
- Make
- Docker or Podman (for container builds)

### Building

```shell
# Build binary
make build

# Build for local development (native OS/arch)
make build-local

# Run tests
make test

# Run linter
make lint

# Build container image
make container VERSION=1.0.0

# Push to registry
make container-push VERSION=1.0.0
```

### Testing Locally

The extension can be tested in a local Talos cluster using `talosctl cluster create` with a custom image that includes the extension.

## Limitations

- Only runs on **control plane nodes** (exits gracefully on workers)
- Requires all control plane nodes to be on the **same network segment/CIDR**
- Does not support **multi-cluster coordination**
- Does not integrate with external service discovery (Consul, etc.)
- **TLS verification is disabled** during peer discovery (required for unknown nodes)
- Network interface selection prefers the first interface with a valid IPv4 address

## Troubleshooting

### Check Extension Logs

```shell
talosctl logs ext-kommodity-autobootstrap
```

### Expected Log Output (Successful Bootstrap)

```
{"level":"info","msg":"starting kommodity-autobootstrap-extension","version":"..."}
{"level":"info","msg":"control plane node detected, starting bootstrap process"}
{"level":"info","msg":"resolved apid endpoint","endpoint":"10.0.0.5:50000"}
{"level":"info","msg":"reading machine CA from STATE partition"}
{"level":"info","msg":"generating admin TLS credentials from machine CA"}
{"level":"info","msg":"connected to apid with admin credentials"}
{"level":"info","msg":"network discovered","localIP":"10.0.0.5","cidr":"10.0.0.0/24","gateway":"10.0.0.1"}
{"level":"info","msg":"peer discovery complete","peers_found":2}
{"level":"info","msg":"leader election complete","leader":"10.0.0.5","is_leader":true,"candidates":3}
{"level":"info","msg":"elected as leader, initiating bootstrap"}
{"level":"info","msg":"waiting before bootstrap","delay":10}
{"level":"info","msg":"executing bootstrap"}
{"level":"info","msg":"waiting for etcd to become ready"}
{"level":"info","msg":"bootstrap successful"}
{"level":"info","msg":"bootstrap service completed successfully"}
```

### Common Issues

| Issue | Cause | Solution |
|-------|-------|----------|
| Extension exits immediately | Worker node detected | Expected behavior - extension only runs on control plane |
| "failed to read machine CA" | STATE partition not accessible | Check `/dev/disk/by-partlabel/STATE` exists |
| "failed to connect to apid" | apid not ready | Extension will retry automatically |
| No peers discovered | Network segmentation | Ensure all nodes are on same CIDR |
| Bootstrap hangs | etcd not starting | Check etcd service logs |

## Compatibility

| Component | Version |
|---|---|
| Talos Linux | >= v1.6.0 |
| Go | 1.24+ |

## License

Talos Auto-Bootstrap Extension is licensed under the [Apache License 2.0](LICENSE).
