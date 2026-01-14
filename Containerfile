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

# Copy binary to Talos extension location
COPY --from=builder /app/bin/talos-auto-bootstrap \
    /usr/local/lib/containers/talos-auto-bootstrap/talos-auto-bootstrap

# Copy service definition
COPY talos-auto-bootstrap.yaml \
    /usr/local/etc/containers/talos-auto-bootstrap.yaml

# Copy extension manifest
COPY manifest.yaml /manifest.yaml
