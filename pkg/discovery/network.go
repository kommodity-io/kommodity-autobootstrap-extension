package discovery

import (
	"context"
	"fmt"
	"net/netip"

	"github.com/cosi-project/runtime/pkg/safe"
	"github.com/cosi-project/runtime/pkg/state"
	"github.com/siderolabs/talos/pkg/machinery/resources/network"
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
