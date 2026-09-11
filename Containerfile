# Build stage
FROM golang:1.24-alpine AS builder

ARG VERSION=dev

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache git make

# Copy go mod files first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Template extension manifest with build version.
# Talos reads metadata.version from this file, not the image tag.
# VERSION may be a semver tag (v1.6.1, with possible -prerelease/+build metadata)
# or 'dev' for local builds. Strip the leading v with POSIX expansion, escape
# sed-special chars (& and \) in the replacement, and use a delimiter that
# cannot appear in a semver string.
RUN ver="${VERSION#v}" \
 && esc_ver=$(printf '%s\n' "$ver" | sed 's/[&\\]/\\&/g') \
 && test -n "$ver" \
 && sed -i "s|__VERSION__|$esc_ver|" manifest.yaml \
 && ! grep -q '__VERSION__' manifest.yaml

# Build the binary using Makefile
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 make build UPX_FLAGS= VERSION=${VERSION}

# Create bind mount point directories under /app/rootfs (mirrors final /rootfs structure).
# These empty directories are required because Talos extension containers use a read-only
# root filesystem, so bind mount destinations must exist in the image.
# See kommodity-autobootstrap.yaml for mount configuration.
RUN mkdir -p \
    /app/rootfs/var/mnt \
    /app/rootfs/system/secrets \
    /app/rootfs/dev \
    /app/rootfs/host/proc \
    /app/rootfs/etc

# Extension stage - Talos system extension format
FROM scratch

# Copy binary to Talos extension location
COPY --from=builder /app/bin/kommodity-autobootstrap-extension \
    /rootfs/usr/local/lib/containers/kommodity-autobootstrap/kommodity-autobootstrap-extension

# Copy bind mount point directories (must exist for read-only container filesystem)
COPY --from=builder /app/rootfs/ /rootfs/

# Copy service definition (under /rootfs/)
COPY kommodity-autobootstrap.yaml \
    /rootfs/usr/local/etc/containers/kommodity-autobootstrap.yaml

# Copy extension manifest (at root, not under /rootfs/)
# Uses the templated manifest from the builder stage.
COPY --from=builder /app/manifest.yaml /manifest.yaml
