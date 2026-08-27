package discovery

import (
	"errors"
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

func TestGenerateIPsInCIDR_Degenerate(t *testing.T) {
	tests := []struct {
		name string
		cidr string
		want int
	}{
		// A /32 has no host range, so it must yield nothing rather than
		// silently producing an empty scan that looks like an empty network.
		{name: "slash32 node address", cidr: "10.0.0.4/32", want: 0},
		{name: "slash31 point to point", cidr: "10.0.0.0/31", want: 0},
		// These two must return immediately rather than allocating; a /0
		// would otherwise try to build ~4.3 billion addresses.
		{name: "slash8 too large", cidr: "10.0.0.0/8", want: 0},
		{name: "slash0 default route", cidr: "0.0.0.0/0", want: 0},
		{name: "slash16 node network", cidr: "10.0.0.0/16", want: 65534},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ips := GenerateIPsInCIDR(netip.MustParsePrefix(tt.cidr))
			if len(ips) != tt.want {
				t.Errorf("expected %d IPs for %s, got %d", tt.want, tt.cidr, len(ips))
			}
		})
	}
}

func TestGenerateIPsInCIDR_Unmasked(t *testing.T) {
	// An unmasked prefix must still generate from the network address.
	ips := GenerateIPsInCIDR(netip.MustParsePrefix("10.0.0.4/16"))

	if len(ips) != 65534 {
		t.Fatalf("expected 65534 IPs, got %d", len(ips))
	}

	if ips[0].String() != "10.0.0.1" {
		t.Errorf("expected first IP to be 10.0.0.1, got %s", ips[0])
	}
}

// procRouteFixture is a real /proc/net/route dump: a default route via
// 172.22.224.1 plus the on-link 172.22.224.0/20 network route.
const procRouteFixture = `Iface	Destination	Gateway 	Flags	RefCnt	Use	Metric	Mask		MTU	Window	IRTT
eth0	00000000	01E016AC	0003	0	0	0	00000000	0	0	0
eth0	00E016AC	00000000	0001	0	0	0	00F0FFFF	0	0	0
`

// slash32RouteFixture is a node with a /32 address whose network is reachable
// only through a gateway. The 10.0.0.0/16 route has gateway 10.0.0.1, and the
// only gateway-less route is the gateway's own /32, so the node network is
// described solely by a route that has a gateway.
const slash32RouteFixture = `Iface	Destination	Gateway 	Flags	RefCnt	Use	Metric	Mask		MTU	Window	IRTT
eth0	00000000	0100000A	0003	0	0	1024	00000000	0	0	0
eth0	0000000A	0100000A	0003	0	0	1024	0000FFFF	0	0	0
eth0	0100000A	00000000	0005	0	0	1024	FFFFFFFF	0	0	0
eth0	FEA9FEA9	0100000A	0007	0	0	1024	FFFFFFFF	0	0	0
`

func TestParseRoutes(t *testing.T) {
	routes := parseRoutes(strings.NewReader(procRouteFixture))

	if len(routes) != 2 {
		t.Fatalf("expected 2 routes, got %d", len(routes))
	}

	if routes[1].dest.String() != "172.22.224.0" {
		t.Errorf("expected destination 172.22.224.0, got %s", routes[1].dest)
	}

	if routes[1].mask.String() != "255.255.240.0" {
		t.Errorf("expected mask 255.255.240.0, got %s", routes[1].mask)
	}

	if routes[0].gateway.String() != "172.22.224.1" {
		t.Errorf("expected gateway 172.22.224.1, got %s", routes[0].gateway)
	}
}

func TestDefaultGatewayFrom(t *testing.T) {
	gw, ok := defaultGatewayFrom(parseRoutes(strings.NewReader(procRouteFixture)))
	if !ok {
		t.Fatal("expected a default gateway")
	}

	if gw.String() != "172.22.224.1" {
		t.Errorf("expected gateway 172.22.224.1, got %s", gw)
	}
}

func TestNetworkPrefixFrom(t *testing.T) {
	const twoOnLink = `Iface	Destination	Gateway 	Flags	RefCnt	Use	Metric	Mask		MTU	Window	IRTT
eth0	0000000A	00000000	0001	0	0	0	0000FFFF	0	0	0
eth0	0000000A	00000000	0001	0	0	0	00FFFFFF	0	0	0
`
	// A node in a /16 subnet of a /8 supernet: the /8 is too wide to scan, so
	// the /16 must win rather than the /8 being selected and refused later.
	const subnetOfSupernet = `Iface	Destination	Gateway 	Flags	RefCnt	Use	Metric	Mask		MTU	Window	IRTT
eth0	00000000	0100010A	0003	0	0	0	00000000	0	0	0
eth0	0000010A	00000000	0001	0	0	0	0000FFFF	0	0	0
eth0	0000000A	0100010A	0003	0	0	0	000000FF	0	0	0
`
	// Ordinary on-link /20 with a real address prefix.
	const onLinkSlash20 = `Iface	Destination	Gateway 	Flags	RefCnt	Use	Metric	Mask		MTU	Window	IRTT
eth0	00000000	0100C80A	0003	0	0	0	00000000	0	0	0
eth0	0000C80A	00000000	0001	0	0	0	00F0FFFF	0	0	0
`
	// A mask of 255.0.255.0 has a hole in it and must be rejected.
	const nonContiguous = `Iface	Destination	Gateway 	Flags	RefCnt	Use	Metric	Mask		MTU	Window	IRTT
eth0	0000000A	00000000	0001	0	0	0	00FF00FF	0	0	0
`

	tests := []struct {
		name    string
		routes  string
		iface   string
		localIP string
		want    string
		wantOK  bool
	}{
		{
			// The network route has a gateway and is still the one that
			// describes the node network.
			name: "slash32 address finds the real network", routes: slash32RouteFixture,
			iface: "eth0", localIP: "10.0.0.4", want: "10.0.0.0/16", wantOK: true,
		},
		{
			name: "on-link slash20 network", routes: onLinkSlash20,
			iface: "eth0", localIP: "10.200.0.5", want: "10.200.0.0/20", wantOK: true,
		},
		{
			name: "subnet wins over unscannable supernet", routes: subnetOfSupernet,
			iface: "eth0", localIP: "10.1.0.5", want: "10.1.0.0/16", wantOK: true,
		},
		{
			name: "ordinary slash20 network", routes: procRouteFixture,
			iface: "eth0", localIP: "172.22.231.151", want: "172.22.224.0/20", wantOK: true,
		},
		{
			name: "no route for this interface", routes: slash32RouteFixture,
			iface: "eth1", localIP: "10.0.0.4", wantOK: false,
		},
		{
			name: "route does not contain the local IP", routes: slash32RouteFixture,
			iface: "eth0", localIP: "192.168.1.5", wantOK: false,
		},
		{
			name: "narrowest matching route wins", routes: twoOnLink,
			iface: "eth0", localIP: "10.0.0.4", want: "10.0.0.0/24", wantOK: true,
		},
		{
			name: "non-contiguous mask is skipped", routes: nonContiguous,
			iface: "eth0", localIP: "10.0.0.4", wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prefix, ok := networkPrefixFrom(
				parseRoutes(strings.NewReader(tt.routes)), tt.iface, netip.MustParseAddr(tt.localIP))
			if ok != tt.wantOK {
				t.Fatalf("expected ok=%v, got %v", tt.wantOK, ok)
			}

			if ok && prefix.String() != tt.want {
				t.Errorf("expected %s, got %s", tt.want, prefix)
			}
		})
	}
}

// A bad override must fail rather than fall back to the detected range. A typo
// that silently reverted to a /32 interface prefix would leave the node scanning
// nothing and bootstrapping alone, which is the case this override exists for.
func TestGetNetworkInfoRejectsBadScanCIDR(t *testing.T) {
	tests := []struct {
		name     string
		scanCIDR string
		wantIn   string
	}{
		{
			name: "not a prefix at all", scanCIDR: "not-a-cidr",
			wantIn: "TALOS_AUTO_BOOTSTRAP_SCAN_CIDR",
		},
		{
			name: "bare address without a prefix length", scanCIDR: "10.0.0.0",
			wantIn: "TALOS_AUTO_BOOTSTRAP_SCAN_CIDR",
		},
		{
			// The range guard would otherwise advise setting the variable that
			// has just been set.
			name: "IPv6 is not scannable", scanCIDR: "fd00::/64",
			wantIn: "IPv6",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := GetNetworkInfo(tt.scanCIDR)
			if err == nil {
				t.Fatalf("expected %q to be refused", tt.scanCIDR)
			}

			if !strings.Contains(err.Error(), tt.wantIn) {
				t.Errorf("error should mention %q, got: %v", tt.wantIn, err)
			}
		})
	}
}

// A valid override replaces the detected range, and is masked so an override
// given as a host address still scans the network it belongs to.
func TestGetNetworkInfoAppliesScanCIDR(t *testing.T) {
	// Deliberately not a range any interface here is on, so the assertion shows
	// the override won rather than coinciding with detection.
	info, err := GetNetworkInfo("10.99.0.4/16")
	if err != nil {
		t.Skipf("no usable interface in this environment: %v", err)
	}

	if info.CIDR.String() != "10.99.0.0/16" {
		t.Errorf("expected the override to win and be masked, got %s", info.CIDR)
	}
}

// GetNetworkInfo reports a missing interface address through a sentinel, so the
// caller can tell an early-boot condition that resolves itself from an operator
// error that will not.
func TestGetNetworkInfoNoNetworkIsSentinel(t *testing.T) {
	// A malformed override is an operator error and must not be mistaken for
	// the transient case.
	_, err := GetNetworkInfo("not-a-cidr")
	if errors.Is(err, ErrNoNetwork) {
		t.Errorf("a bad override must not report ErrNoNetwork, got %v", err)
	}
}
