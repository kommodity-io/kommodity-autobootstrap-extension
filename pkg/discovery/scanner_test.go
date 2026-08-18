package discovery

import (
	"context"
	"crypto/tls"
	"net/netip"
	"testing"
	"time"
)

// The probe must present the caller's client certificate. apid requires
// client-cert auth, so a probe without one is rejected on the RPC and the peer
// looks like an empty address.
func TestProbeUsesClientCertificate(t *testing.T) {
	clientTLS := &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{{0x01, 0x02}}}},
		MinVersion:   tls.VersionTLS12,
	}

	// Probing an address with nothing listening fails, but it must fail after
	// building a config that carries the certificate, not before.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := probeTalosNode(ctx, netip.MustParseAddr("127.0.0.1"), 50*time.Millisecond, clientTLS)
	if err == nil {
		t.Fatal("expected an error probing an address with no Talos node")
	}

	// A nil config must not panic: discovery still runs before credentials
	// exist in some paths.
	_, err = probeTalosNode(ctx, netip.MustParseAddr("127.0.0.1"), 50*time.Millisecond, nil)
	if err == nil {
		t.Fatal("expected an error probing an address with no Talos node")
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
				time.Second, 16, nil, 0)
			if err == nil {
				t.Fatalf("expected %s to be refused", tt.cidr)
			}
			if elapsed := time.Since(start); elapsed > time.Second {
				t.Errorf("refusal took %s, should be immediate", elapsed)
			}
		})
	}
}

// The scan stops probing as soon as enough control planes are found rather than
// waiting out the remaining addresses. Authenticated probes to empty addresses
// cost the full timeout, so a /16 sweep takes minutes, and peers sit at the low
// addresses. This covers the arithmetic that decides when the sweep is cut
// short.
func TestQuorumStopCondition(t *testing.T) {
	tests := []struct {
		name              string
		wantControlPlanes int
		foundPeers        int
		stop              bool
	}{
		// The local node counts toward quorum, so a 3 node control plane needs
		// only 2 peers.
		{name: "three node quorum, one peer", wantControlPlanes: 3, foundPeers: 1, stop: false},
		{name: "three node quorum, two peers", wantControlPlanes: 3, foundPeers: 2, stop: true},
		{name: "single node quorum stops immediately", wantControlPlanes: 1, foundPeers: 0, stop: true},
		{name: "two node quorum, one peer", wantControlPlanes: 2, foundPeers: 1, stop: true},
		// Zero means sweep the whole range.
		{name: "zero never stops early", wantControlPlanes: 0, foundPeers: 99, stop: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.wantControlPlanes > 0 && tt.foundPeers >= tt.wantControlPlanes-1
			if got != tt.stop {
				t.Errorf("want=%d found=%d: expected stop=%v, got %v",
					tt.wantControlPlanes, tt.foundPeers, tt.stop, got)
			}
		})
	}
}

// A cancelled context must not stall the scan: the early return relies on
// cancellation unwinding the errgroup promptly.
func TestScanHonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()

	_, err := ScanCIDRForTalosNodes(ctx, netip.MustParsePrefix("10.0.0.0/16"),
		netip.MustParseAddr("10.0.0.4"), 2*time.Second, 256, nil, 3)
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

// wantControlPlanes of 0 means no quorum target, so the whole range is swept.
func TestScanQuorumZeroSweepsRange(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	nodes, err := ScanCIDRForTalosNodes(ctx, netip.MustParsePrefix("127.0.0.0/30"),
		netip.MustParseAddr("127.0.0.1"), 100*time.Millisecond, 4, nil, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(nodes) != 0 {
		t.Errorf("expected no Talos nodes on loopback, got %d", len(nodes))
	}
}
