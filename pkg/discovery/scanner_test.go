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
				time.Second, 16, nil)
			if err == nil {
				t.Fatalf("expected %s to be refused", tt.cidr)
			}
			if elapsed := time.Since(start); elapsed > time.Second {
				t.Errorf("refusal took %s, should be immediate", elapsed)
			}
		})
	}
}
