package discovery

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strings"

	"github.com/cosi-project/runtime/pkg/safe"
	"github.com/cosi-project/runtime/pkg/state"
	"github.com/siderolabs/talos/pkg/machinery/resources/network"
)

const (
	// maxScanPrefixBits is the narrowest accepted scan CIDR prefix.
	// Prefixes narrower than /30 yield fewer than two host addresses, so
	// peer discovery can never reach quorum.
	maxScanPrefixBits = 30

	// minScanPrefixBits is the widest accepted scan CIDR prefix.
	// Prefixes wider than /16 span multiple subnets and can produce an
	// unbounded number of probe targets, risking exhaustion and long scans.
	minScanPrefixBits = 16
)

// NetworkInfo holds discovered network configuration from COSI resources.
type NetworkInfo struct {
	// LocalIP is this node's IP address
	LocalIP netip.Addr
	// CIDR is the network prefix (e.g., 192.168.1.0/24)
	CIDR netip.Prefix
	// Gateway is the default gateway address
	Gateway netip.Addr
	// LinkName is the network interface name
	LinkName string
}

// GetNetworkInfoFromCOSI retrieves network configuration from Talos COSI state.
func GetNetworkInfoFromCOSI(ctx context.Context, st state.State) (*NetworkInfo, error) {
	addresses, err := safe.StateListAll[*network.AddressStatus](ctx, st)
	if err != nil {
		return nil, fmt.Errorf("failed to list addresses: %w", err)
	}

	var info *NetworkInfo

	for addr := range addresses.All() {
		spec := addr.TypedSpec()

		// Skip loopback, link-local, and IPv6 addresses
		if spec.Address.Addr().IsLoopback() ||
			spec.Address.Addr().IsLinkLocalUnicast() ||
			spec.Address.Addr().Is6() {
			continue
		}

		info = &NetworkInfo{
			LocalIP:  spec.Address.Addr(),
			CIDR:     spec.Address.Masked(),
			LinkName: spec.LinkName,
		}
		break
	}

	if info == nil {
		return nil, fmt.Errorf("no suitable network address found")
	}

	// Get default gateway from routes
	routes, err := safe.StateListAll[*network.RouteStatus](ctx, st)
	if err != nil {
		return nil, fmt.Errorf("failed to list routes: %w", err)
	}

	for route := range routes.All() {
		spec := route.TypedSpec()
		// Default route has destination with 0 bits (0.0.0.0/0)
		if spec.Destination.Bits() == 0 && spec.Gateway.IsValid() {
			info.Gateway = spec.Gateway
			break
		}
	}

	return info, nil
}

// GetNetworkInfo retrieves network configuration using Go's net package.
// This is used instead of COSI when COSI access is not available.
func GetNetworkInfo() (*NetworkInfo, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("failed to get network interfaces: %w", err)
	}

	var info *NetworkInfo

	for _, iface := range interfaces {
		// Skip loopback and down interfaces
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}

			ip := ipNet.IP.To4()
			if ip == nil {
				continue // Skip IPv6
			}

			// Skip loopback and link-local
			if ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}

			netipAddr, ok := netip.AddrFromSlice(ip)
			if !ok {
				continue
			}

			ones, _ := ipNet.Mask.Size()
			prefix := netip.PrefixFrom(netipAddr, ones)

			info = &NetworkInfo{
				LocalIP:  netipAddr,
				CIDR:     prefix.Masked(),
				LinkName: iface.Name,
			}
			break
		}
		if info != nil {
			break
		}
	}

	if info == nil {
		return nil, fmt.Errorf("no suitable network address found")
	}

	// Get default gateway from /proc/net/route
	gateway, err := getDefaultGateway()
	if err == nil {
		info.Gateway = gateway
	}

	return info, nil
}

// ParseScanCIDR parses and validates an operator-supplied scan CIDR override.
// It trims surrounding whitespace, requires a valid IPv4 CIDR within the
// scannable prefix range, and returns the masked prefix. A malformed,
// non-IPv4, or out-of-range value returns an error.
func ParseScanCIDR(override string) (netip.Prefix, error) {
	trimmed := strings.TrimSpace(override)
	prefix, err := netip.ParsePrefix(trimmed)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("failed to parse scan CIDR override %q: %w", override, err)
	}
	if !prefix.Addr().Is4() {
		return netip.Prefix{}, fmt.Errorf("scan CIDR override %q must be IPv4", override)
	}
	if bits := prefix.Bits(); bits > maxScanPrefixBits || bits < minScanPrefixBits {
		return netip.Prefix{}, fmt.Errorf("scan CIDR override %q must be between /%d and /%d",
			override, minScanPrefixBits, maxScanPrefixBits)
	}
	return prefix.Masked(), nil
}

// getDefaultGateway reads the default gateway from /proc/net/route.
// Uses /host/proc when running in container to avoid conflicting with container's /proc.
func getDefaultGateway() (netip.Addr, error) {
	// Try /host/proc first (container environment), then /proc (native)
	file, err := os.Open("/host/proc/net/route")
	if err != nil {
		file, err = os.Open("/proc/net/route")
	}
	if err != nil {
		return netip.Addr{}, err
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	// Skip header line
	scanner.Scan()

	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 {
			continue
		}

		// Destination is field 1, Gateway is field 2
		// Default route has destination 00000000
		if fields[1] == "00000000" {
			// Gateway is in hex, little-endian
			var gw uint32
			if _, err := fmt.Sscanf(fields[2], "%x", &gw); err != nil {
				continue
			}
			// Convert from little-endian
			gwBytes := make([]byte, 4)
			binary.LittleEndian.PutUint32(gwBytes, gw)
			addr, ok := netip.AddrFromSlice(gwBytes)
			if ok {
				return addr, nil
			}
		}
	}

	return netip.Addr{}, fmt.Errorf("no default gateway found")
}

// GenerateIPsInCIDR generates all host IP addresses within a CIDR range,
// excluding network and broadcast addresses.
func GenerateIPsInCIDR(cidr netip.Prefix) []netip.Addr {
	var ips []netip.Addr

	addr := cidr.Addr()
	bits := cidr.Bits()
	hostBits := 32 - bits
	numHosts := 1 << hostBits

	// Skip network address (i=0) and broadcast address (i=numHosts-1)
	for i := 1; i < numHosts-1; i++ {
		ip := addToIP(addr, i)
		if ip.IsValid() && cidr.Contains(ip) {
			ips = append(ips, ip)
		}
	}

	return ips
}

// addToIP adds an offset to an IPv4 address.
func addToIP(ip netip.Addr, offset int) netip.Addr {
	bytes := ip.As4()
	val := uint32(bytes[0])<<24 | uint32(bytes[1])<<16 |
		uint32(bytes[2])<<8 | uint32(bytes[3])
	val += uint32(offset)
	return netip.AddrFrom4([4]byte{
		byte(val >> 24), byte(val >> 16), byte(val >> 8), byte(val),
	})
}
