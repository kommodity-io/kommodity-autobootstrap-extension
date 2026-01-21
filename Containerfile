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

# Build the binary using Makefile
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 make build UPX_FLAGS= VERSION=${VERSION}

# Extension stage - Talos system extension format
FROM scratch

# Copy binary to Talos extension location (under /rootfs/)
COPY --from=builder /app/bin/kommodity-autobootstrap-extension \
    /rootfs/usr/local/lib/containers/kommodity-autobootstrap/kommodity-autobootstrap-extension

# Copy service definition (under /rootfs/)
COPY kommodity-autobootstrap.yaml \
    /rootfs/usr/local/etc/containers/kommodity-autobootstrap.yaml

# Copy extension manifest (at root, not under /rootfs/)
COPY manifest.yaml /manifest.yaml
