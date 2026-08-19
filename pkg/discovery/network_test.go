package discovery

import (
	"net/netip"
	"strings"
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

func TestParseScanCIDR(t *testing.T) {
	tests := []struct {
		name        string
		override    string
		expected    string
		wantError   bool
		errContains string
	}{
		{
			name:     "valid IPv4 CIDR",
			override: "10.200.48.0/24",
			expected: "10.200.48.0/24",
		},
		{
			name:     "unmasked host bits are masked",
			override: "10.200.48.5/24",
			expected: "10.200.48.0/24",
		},
		{
			name:     "whitespace is trimmed",
			override: "  10.200.48.0/24  ",
			expected: "10.200.48.0/24",
		},
		{
			name:     "slash30 accepted at narrow boundary",
			override: "10.200.48.0/30",
			expected: "10.200.48.0/30",
		},
		{
			name:     "slash16 accepted at wide boundary",
			override: "10.0.0.0/16",
			expected: "10.0.0.0/16",
		},
		{
			name:        "slash31 rejected as too narrow",
			override:    "10.200.48.0/31",
			wantError:   true,
			errContains: "must be between",
		},
		{
			name:        "slash32 rejected as too narrow",
			override:    "10.200.48.4/32",
			wantError:   true,
			errContains: "must be between",
		},
		{
			name:        "slash15 rejected as too wide",
			override:    "10.0.0.0/15",
			wantError:   true,
			errContains: "must be between",
		},
		{
			name:        "slash0 rejected as too wide",
			override:    "0.0.0.0/0",
			wantError:   true,
			errContains: "must be between",
		},
		{
			name:        "IPv6 CIDR rejected",
			override:    "fd00::/64",
			wantError:   true,
			errContains: "must be IPv4",
		},
		{
			name:      "malformed value rejected",
			override:  "not-a-cidr",
			wantError: true,
		},
		{
			name:      "invalid prefix length rejected",
			override:  "10.200.48.0/33",
			wantError: true,
		},
		{
			name:      "empty string rejected",
			override:  "",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParseScanCIDR(tt.override)
			if tt.wantError {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("expected error to contain %q, got %q", tt.errContains, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.String() != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, result.String())
			}
		})
	}
}
