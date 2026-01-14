package discovery

import (
	"net/netip"
	"testing"
)

func TestGenerateIPsInCIDR_ClassC(t *testing.T) {
	cidr := netip.MustParsePrefix("192.168.1.0/24")
	ips := GenerateIPsInCIDR(cidr)

	// Class C has 256 addresses, minus network (0) and broadcast (255) = 254 hosts
	if len(ips) != 254 {
		t.Errorf("expected 254 IPs for /24, got %d", len(ips))
	}

	// First IP should be .1
	if ips[0].String() != "192.168.1.1" {
		t.Errorf("expected first IP to be 192.168.1.1, got %s", ips[0])
	}

	// Last IP should be .254
	if ips[len(ips)-1].String() != "192.168.1.254" {
		t.Errorf("expected last IP to be 192.168.1.254, got %s", ips[len(ips)-1])
	}
}

func TestGenerateIPsInCIDR_Slash28(t *testing.T) {
	cidr := netip.MustParsePrefix("10.0.0.0/28")
	ips := GenerateIPsInCIDR(cidr)

	// /28 has 16 addresses, minus network and broadcast = 14 hosts
	if len(ips) != 14 {
		t.Errorf("expected 14 IPs for /28, got %d", len(ips))
	}

	// First IP should be .1
	if ips[0].String() != "10.0.0.1" {
		t.Errorf("expected first IP to be 10.0.0.1, got %s", ips[0])
	}

	// Last IP should be .14
	if ips[len(ips)-1].String() != "10.0.0.14" {
		t.Errorf("expected last IP to be 10.0.0.14, got %s", ips[len(ips)-1])
	}
}

func TestGenerateIPsInCIDR_Slash30(t *testing.T) {
	// Point-to-point link (/30 gives 2 usable hosts)
	cidr := netip.MustParsePrefix("172.16.0.0/30")
	ips := GenerateIPsInCIDR(cidr)

	// /30 has 4 addresses, minus network and broadcast = 2 hosts
	if len(ips) != 2 {
		t.Errorf("expected 2 IPs for /30, got %d", len(ips))
	}

	if ips[0].String() != "172.16.0.1" {
		t.Errorf("expected first IP to be 172.16.0.1, got %s", ips[0])
	}

	if ips[1].String() != "172.16.0.2" {
		t.Errorf("expected second IP to be 172.16.0.2, got %s", ips[1])
	}
}

func TestAddToIP(t *testing.T) {
	tests := []struct {
		name     string
		ip       string
		offset   int
		expected string
	}{
		{
			name:     "add 1",
			ip:       "192.168.1.0",
			offset:   1,
			expected: "192.168.1.1",
		},
		{
			name:     "add 254",
			ip:       "192.168.1.0",
			offset:   254,
			expected: "192.168.1.254",
		},
		{
			name:     "overflow to next octet",
			ip:       "192.168.1.255",
			offset:   1,
			expected: "192.168.2.0",
		},
		{
			name:     "add zero",
			ip:       "10.0.0.5",
			offset:   0,
			expected: "10.0.0.5",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := netip.MustParseAddr(tt.ip)
			result := addToIP(ip, tt.offset)
			if result.String() != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, result)
			}
		})
	}
}
