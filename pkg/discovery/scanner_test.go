package discovery

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Probing without a machine CA is refused outright. Falling back to an
// unverified dial would leave nothing to distinguish a peer from anything else
// answering on the port, so the caller has to supply one.
func TestProbeRequiresMachineCA(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	ip := netip.MustParseAddr("127.0.0.1")

	for _, tt := range []struct {
		name      string
		clientTLS *tls.Config
	}{
		{name: "no config at all", clientTLS: nil},
		{name: "config without a CA", clientTLS: &tls.Config{MinVersion: tls.VersionTLS12}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := probeTalosNode(ctx, ip, 50*time.Millisecond, tt.clientTLS)
			if err == nil {
				t.Fatal("expected the probe to be refused")
			}

			// Refused before dialling, not by whatever the dial happened to hit.
			if !strings.Contains(err.Error(), "machine CA") {
				t.Errorf("expected a refusal naming the missing CA, got: %v", err)
			}
		})
	}
}

// Guard rejection happens before any dialling, so a bad range fails fast.
func TestScanRejectsUnusableRangeBeforeProbing(t *testing.T) {
	tests := []struct {
		name string
		cidr string
	}{
		{name: "slash32 has no host range", cidr: "10.0.0.4/32"},
		{name: "slash8 is too large to scan", cidr: "10.0.0.0/8"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start := time.Now()
			_, err := ScanCIDRForTalosNodes(context.Background(),
				netip.MustParsePrefix(tt.cidr), netip.MustParseAddr("10.0.0.4"),
				time.Second, 16, nil)
			if err == nil {
				t.Fatalf("expected %s to be refused", tt.cidr)
			}
			if elapsed := time.Since(start); elapsed > time.Second {
				t.Errorf("refusal took %s, should be immediate", elapsed)
			}

			// The caller logs these at Warn rather than Error on the strength
			// of the sentinel, so it has to survive the wrapping above.
			if !errors.Is(err, ErrUnusableRange) {
				t.Errorf("expected ErrUnusableRange, got %v", err)
			}
		})
	}
}

// A cancelled context must not stall the scan: without cancellation unwinding
// the errgroup promptly, a caller giving up would still wait out the sweep.
func TestScanHonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()

	_, err := ScanCIDRForTalosNodes(ctx, netip.MustParsePrefix("10.0.0.0/16"),
		netip.MustParseAddr("10.0.0.4"), 2*time.Second, 256, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Without cancellation this range would take minutes.
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("cancelled scan took %s, expected prompt return", elapsed)
	} else {
		t.Logf("cancelled scan returned in %s", elapsed)
	}
}

// A range where nothing answers yields no nodes rather than an error: an
// address with no Talos node is the ordinary case, not a scan failure.
func TestScanSweepsRange(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	nodes, err := ScanCIDRForTalosNodes(ctx, netip.MustParsePrefix("127.0.0.0/30"),
		netip.MustParseAddr("127.0.0.1"), 100*time.Millisecond, 4, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(nodes) != 0 {
		t.Errorf("expected no Talos nodes on loopback, got %d", len(nodes))
	}
}

// A sweep over addresses with nothing listening must stay silent: that is the
// ordinary shape of a scan, and warning about it would fire on every healthy
// run, including a single control plane that legitimately finds no peers.
func TestScanIsQuietWhenNobodyAnswers(t *testing.T) {
	core, logs := observer.New(zapcore.WarnLevel)
	restore := zap.ReplaceGlobals(zap.New(core))

	defer restore()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	_, err := ScanCIDRForTalosNodes(ctx, netip.MustParsePrefix("127.0.0.0/30"),
		netip.MustParseAddr("127.0.0.1"), 100*time.Millisecond, 4,
		&tls.Config{RootCAs: x509.NewCertPool(), MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if n := logs.Len(); n != 0 {
		t.Errorf("expected no warnings scanning empty addresses, got %d: %v",
			n, logs.All())
	}
}

// A peer that answers and then refuses us is the one probe failure worth
// reporting, and the ordinary failures must stay silent. Warning on those would
// fire on every address in a healthy sweep, which is most of them.
func TestReportProbeError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		warn bool
	}{
		{
			name: "refused by RBAC",
			err:  status.Error(codes.PermissionDenied, "denied"),
			warn: true,
		},
		{
			name: "certificate not accepted",
			err:  status.Error(codes.Unauthenticated, "no valid certificate"),
			warn: true,
		},
		{
			// The common case: nothing listening on the address.
			name: "connection refused",
			err:  status.Error(codes.Unavailable, "connection refused"),
			warn: false,
		},
		{
			name: "unroutable address",
			err:  status.Error(codes.DeadlineExceeded, "context deadline exceeded"),
			warn: false,
		},
		{
			name: "not a gRPC error at all",
			err:  errors.New("some other failure"),
			warn: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			core, logs := observer.New(zapcore.WarnLevel)
			restore := zap.ReplaceGlobals(zap.New(core))

			defer restore()

			reportProbeError(netip.MustParseAddr("10.0.0.7"), tt.err)

			if got := logs.Len() > 0; got != tt.warn {
				t.Fatalf("expected warn=%v, got %v (%v)", tt.warn, got, logs.All())
			}

			if !tt.warn {
				return
			}

			// The address is what makes the warning actionable.
			if entry := logs.All()[0]; !strings.Contains(
				entry.ContextMap()["ip"].(string), "10.0.0.7") {
				t.Errorf("warning should name the peer, got %v", entry.ContextMap())
			}
		})
	}
}
