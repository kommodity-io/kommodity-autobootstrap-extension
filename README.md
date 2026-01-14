# Talos Auto-Bootstrap Extension

[![Go Report Card](https://img.shields.io/badge/go%20report-A+-brightgreen?style=flat-square)](https://goreportcard.com/report/github.com/kommodity-io/talos-auto-bootstrap)
[![Go Reference](https://img.shields.io/badge/godoc-reference-blue?style=flat-square)](https://pkg.go.dev/github.com/kommodity-io/talos-auto-bootstrap)
[![CI](https://img.shields.io/github/actions/workflow/status/kommodity-io/talos-auto-bootstrap/release.yml?branch=main&label=ci&style=flat-square)](https://github.com/kommodity-io/talos-auto-bootstrap/actions)
[![Release](https://img.shields.io/github/v/release/kommodity-io/talos-auto-bootstrap?include_prereleases&label=release&style=flat-square)](https://github.com/kommodity-io/talos-auto-bootstrap/releases)
[![License](https://img.shields.io/github/license/kommodity-io/talos-auto-bootstrap?style=flat-square)](https://github.com/kommodity-io/talos-auto-bootstrap/blob/main/LICENSE)

A Talos Linux system extension that automatically bootstraps Kubernetes clusters without manual intervention.

> :construction: EXPERIMENTAL :construction:: This project is in an early stage of development and is not yet ready for production use. APIs may break between minor releases. The project does however adhere to [semantic versioning](https://semver.org), so patch releases will never break the API.

## Architecture

```
┌──────────────────────────────────────────────────────────────────────────┐
│                         TALOS AUTO-BOOTSTRAP                             │
│                                                                          │
│  ┌────────────────────────────────────────────────────────────────────┐  │
│  │                         STARTUP PHASE                              │  │
│  │                                                                    │  │
│  │  1. Wait for machined socket                                       │  │
│  │  2. Create local Talos client                                      │  │
│  │  3. Check machine type (control plane vs worker)                   │  │
│  │  4. Exit if worker node                                            │  │
│  └────────────────────────────────────────────────────────────────────┘  │
│                                    │                                     │
│                                    ▼                                     │
│  ┌────────────────────────────────────────────────────────────────────┐  │
│  │                      MAIN DISCOVERY LOOP                           │  │
│  │                                                                    │  │
│  │  ┌──────────────┐    ┌──────────────┐    ┌──────────────────────┐  │  │
│  │  │   Network    │    │    CIDR      │    │   Node Detection     │  │  │
│  │  │   Discovery  │───▶│   Scanner    │───▶│   & Validation       │  │  │
│  │  │              │    │              │    │                      │  │  │
│  │  │ Get CIDR     │    │ Probe :50000 │    │ Get machine type     │  │  │
│  │  │ from COSI    │    │ on each IP   │    │ Get boot time        │  │  │
│  │  └──────────────┘    └──────────────┘    └──────────────────────┘  │  │
│  │                                                   │                │  │
│  │                                                   ▼                │  │
│  │  ┌────────────────────────────────────────────────────────────────┐│  │
│  │  │                    LEADER ELECTION                             ││  │
│  │  │                                                                ││  │
│  │  │  1. Collect all control plane nodes                            ││  │
│  │  │  2. Sort by boot time (ascending)                              ││  │
│  │  │  3. Tie-break by IP address (lowest wins)                      ││  │
│  │  │  4. First node in sorted list = LEADER                         ││  │
│  │  └────────────────────────────────────────────────────────────────┘│  │
│  └────────────────────────────────────────────────────────────────────┘  │
│                                    │                                     │
│                    ┌───────────────┴───────────────┐                     │
│                    ▼                               ▼                     │
│  ┌─────────────────────────────┐  ┌─────────────────────────────────┐    │
│  │      LEADER PATH            │  │       FOLLOWER PATH             │    │
│  │                             │  │                                 │    │
│  │ 1. Pre-bootstrap delay      │  │ 1. Wait for check interval      │    │
│  │ 2. Verify still leader      │  │ 2. Check if bootstrapped        │    │
│  │ 3. Check not bootstrapped   │  │ 3. If yes: exit success         │    │
│  │ 4. Execute Bootstrap()      │  │ 4. If no: re-run discovery      │    │
│  │ 5. Wait for etcd ready      │  │                                 │    │
│  │ 6. Exit success             │  │                                 │    │
│  └─────────────────────────────┘  └─────────────────────────────────┘    │
└──────────────────────────────────────────────────────────────────────────┘
```

## Development

Make sure to have a recent version of Go installed. We recommend using [gvm](https://github.com/moovweb/gvm) to install Go.

```shell
gvm install go1.24.2 -B
gvm use go1.24.2 --default
```

As a build system, we use `make`.

```shell
# Create a binary in the `bin/` directory.
make build

# Build for local development (native OS/arch).
make build-local

# Run unit tests.
make test

# Run tests with coverage report.
make test-coverage

# Run linter (requires golangci-lint).
make lint

# Format code.
make fmt

# Run all checks (fmt, vet, lint, test).
make check

# Build container image (supports docker/podman via CONTAINER_RUNTIME).
make container VERSION=1.0.0

# Push container image to registry.
make container-push VERSION=1.0.0

# Clean build artifacts.
make clean
```

## Features

### :zap: Automatic Bootstrap

Eliminates the need for operators to manually run `talosctl bootstrap`. The extension automatically discovers peers, elects a leader, and bootstraps the cluster when all control plane nodes are ready.

### :mag: Peer Discovery

Discovers peer Talos nodes via CIDR network scanning. The extension:

- Extracts network configuration from COSI resources
- Scans the local CIDR range for other Talos nodes on port 50000
- Identifies control plane vs worker nodes
- Retrieves boot time for leader election

### :crown: Deterministic Leader Election

Implements a deterministic leader election algorithm:

1. Collect all control plane nodes
2. Sort by boot time (oldest first)
3. Tie-break by IP address (lowest wins)
4. First node in sorted list becomes leader

This ensures the same leader is elected given the same conditions, preventing race conditions.

### :shield: Safe Bootstrap Coordination

The leader performs multiple safety checks before bootstrapping:

- Pre-bootstrap delay to allow other nodes to participate
- Final verification that cluster hasn't already been bootstrapped
- Waits for etcd to become ready after bootstrap

### :repeat: Fault Tolerance

- Retries indefinitely on transient failures
- Exponential backoff with configurable maximum
- Graceful handling of worker nodes (exits cleanly)
- Continues operation if some peers are unreachable

## :wrench: Configuration

The extension is configured via environment variables:

| Environment Variable | Description | Default Value |
|---|---|---|
| `TALOS_AUTO_BOOTSTRAP_LOG_LEVEL` | Logging verbosity: debug, info, warn, error | `info` |
| `TALOS_AUTO_BOOTSTRAP_SCAN_INTERVAL` | Interval between network discovery scans | `30s` |
| `TALOS_AUTO_BOOTSTRAP_FOLLOWER_CHECK_INTERVAL` | How often followers check bootstrap status | `15s` |
| `TALOS_AUTO_BOOTSTRAP_MIN_NODES` | Minimum control plane nodes for quorum | `1` |
| `TALOS_AUTO_BOOTSTRAP_PRE_BOOTSTRAP_DELAY` | Leader wait time before executing bootstrap | `10s` |
| `TALOS_AUTO_BOOTSTRAP_MAX_BACKOFF` | Maximum retry backoff duration | `2m` |
| `TALOS_AUTO_BOOTSTRAP_SCAN_TIMEOUT` | Timeout for probing each node | `2s` |
| `TALOS_AUTO_BOOTSTRAP_SCAN_CONCURRENCY` | Maximum concurrent node probes | `50` |

## :rocket: Deployment

### Installation

Add the extension to your Talos machine configuration:

```yaml
machine:
  install:
    extensions:
      - image: ghcr.io/kommodity-io/talos-auto-bootstrap:v1.0.0
```

### Configuration Example

Configure the extension for a 3-node control plane cluster:

```yaml
machine:
  install:
    extensions:
      - image: ghcr.io/kommodity-io/talos-auto-bootstrap:v1.0.0
  env:
    TALOS_AUTO_BOOTSTRAP_LOG_LEVEL: info
    TALOS_AUTO_BOOTSTRAP_MIN_NODES: "3"
    TALOS_AUTO_BOOTSTRAP_PRE_BOOTSTRAP_DELAY: "20s"
```

### Building the Extension Image

```shell
# Build and tag the extension image (use CONTAINER_RUNTIME=podman for Podman)
make container VERSION=1.0.0

# Push to your registry
make container-push VERSION=1.0.0 IMAGE=your-registry/talos-auto-bootstrap
```

## :no_entry: Limitations

- Only works on **control plane nodes** (exits gracefully on workers)
- Requires all nodes to be on the **same network segment/CIDR**
- Does not support **multi-cluster coordination**
- Does not integrate with external service discovery (Consul, etcd, etc.)
- **TLS verification is disabled** during discovery phase (required for unknown nodes)

## :compass: Compatibility

| Component | Version |
|---|---|
| Talos Linux | >= v1.6.0 |
| Go | 1.22+ |
| Talos Machinery SDK | v1.11.0 |

## :scroll: License

Talos Auto-Bootstrap Extension is licensed under the [Apache License 2.0](LICENSE).
