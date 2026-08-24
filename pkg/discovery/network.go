package discovery

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"strings"

	"go.uber.org/zap"
)

// NetworkInfo holds discovered network configuration.
type NetworkInfo struct {
	// LocalIP is this node's IP address
	LocalIP netip.Addr
	// CIDR is the network prefix to scan for peers (e.g., 192.168.1.0/24)
	CIDR netip.Prefix
	// Gateway is the default gateway address
	Gateway netip.Addr
	// LinkName is the network interface name
	LinkName string
}

// procRoute is a single parsed row of /proc/net/route.
type procRoute struct {
	iface   string
	dest    netip.Addr
	mask    netip.Addr
	gateway netip.Addr
}

// GetNetworkInfo retrieves network configuration using Go's net package.
// This is used instead of COSI when COSI access is not available.
//
// The scan CIDR is resolved in order: the scanCIDR override, a route describing
// the node network, then the interface address prefix. Some clouds assign /32
// node addresses, where the interface prefix contains no other hosts and only
// the route describes the real network.
func GetNetworkInfo(scanCIDR string) (*NetworkInfo, error) {
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

	routes := readProcRoutes()

	// Gateway is informational only, so failure is not fatal.
	if gw, ok := defaultGatewayFrom(routes); ok {
		info.Gateway = gw
	}

	switch {
	case scanCIDR != "":
		// Fail rather than fall back: a typo here would leave the node scanning
		// a /32 and bootstrapping alone.
		prefix, err := netip.ParsePrefix(scanCIDR)
		if err != nil {
			return nil, fmt.Errorf("invalid TALOS_AUTO_BOOTSTRAP_SCAN_CIDR %q: %w", scanCIDR, err)
		}

		info.CIDR = prefix.Masked()
	default:
		if prefix, ok := networkPrefixFrom(routes, info.LinkName, info.LocalIP); ok {
			info.CIDR = prefix
		}
	}

	return info, nil
}

// readProcRoutes reads and parses the kernel routing table.
// Uses /host/proc when running in a container to avoid conflicting with the
// container's own /proc.
//
// An unreadable routing table is reported rather than returned as an empty
// list. Without routes the scan range falls back to the interface prefix, which
// on a platform that hands out a /32 node address contains no other hosts: the
// node then scans nothing, finds no peers and looks like it is alone. That is
// the failure this package exists to avoid, and it is silent unless said here.
func readProcRoutes() []procRoute {
	file, err := os.Open("/host/proc/net/route")
	if err != nil {
		file, err = os.Open("/proc/net/route")
	}

	if err != nil {
		zap.L().Warn("could not read the kernel routing table, "+
			"falling back to the interface address prefix", zap.Error(err))

		return nil
	}

	defer func() { _ = file.Close() }()

	return parseRoutes(file)
}

// parseRoutes parses /proc/net/route rows. Fields are hex little-endian:
// 0=interface, 1=destination, 2=gateway, 7=mask.
func parseRoutes(r io.Reader) []procRoute {
	var routes []procRoute

	scanner := bufio.NewScanner(r)
	// Skip header line
	scanner.Scan()

	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 8 {
			continue
		}

		dest, ok := hexLEAddr(fields[1])
		if !ok {
			continue
		}

		gateway, ok := hexLEAddr(fields[2])
		if !ok {
			continue
		}

		mask, ok := hexLEAddr(fields[7])
		if !ok {
			continue
		}

		routes = append(routes, procRoute{
			iface:   fields[0],
			dest:    dest,
			mask:    mask,
			gateway: gateway,
		})
	}

	return routes
}

// defaultGatewayFrom returns the gateway of the default route (destination 0.0.0.0).
func defaultGatewayFrom(routes []procRoute) (netip.Addr, bool) {
	for _, route := range routes {
		if route.dest.IsUnspecified() && !route.gateway.IsUnspecified() {
			return route.gateway, true
		}
	}

	return netip.Addr{}, false
}

// networkPrefixFrom returns the prefix of the route for iface that describes
// the node network: a route to a network (not the default route) whose range
// contains localIP.
//
// The route may or may not have a gateway. Some clouds hand out a /32 node
// address and reach the rest of the network through a gateway, so requiring a
// gateway-less route would miss the only route that names the real network.
// Ranges outside the scannable bounds are ignored rather than selected and then
// refused, so a node seeing both a usable and an unusable range picks the usable
// one. When several routes match, the longest (narrowest) prefix wins.
func networkPrefixFrom(routes []procRoute, iface string, localIP netip.Addr) (netip.Prefix, bool) {
	var (
		best  netip.Prefix
		found bool
	)

	for _, route := range routes {
		if route.iface != iface || route.dest.IsUnspecified() {
			continue
		}

		maskBytes := route.mask.As4()

		ones, bits := net.IPMask(maskBytes[:]).Size()
		// Size reports 0,0 for a non-contiguous mask.
		if bits == 0 || ones < minScanPrefixBits || ones > maxScanPrefixBits {
			continue
		}

		prefix := netip.PrefixFrom(route.dest, ones).Masked()
		if !prefix.Contains(localIP) {
			continue
		}

		if !found || prefix.Bits() > best.Bits() {
			best = prefix
			found = true
		}
	}

	return best, found
}

// hexLEAddr decodes a /proc/net/route hex little-endian IPv4 field.
func hexLEAddr(s string) (netip.Addr, bool) {
	var val uint32
	if _, err := fmt.Sscanf(s, "%x", &val); err != nil {
		return netip.Addr{}, false
	}

	buf := make([]byte, 4)
	binary.LittleEndian.PutUint32(buf, val)

	return netip.AddrFromSlice(buf)
}

// GenerateIPsInCIDR generates all host IP addresses within a CIDR range,
// excluding network and broadcast addresses. Prefixes outside /16../30 return
// nothing; ScanCIDRForTalosNodes rejects those with a clear error first.
func GenerateIPsInCIDR(cidr netip.Prefix) []netip.Addr {
	// 10.0.0.4/16 must generate from 10.0.0.1, not 10.0.0.5.
	cidr = cidr.Masked()

	bits := cidr.Bits()
	if !cidr.Addr().Is4() || bits < minScanPrefixBits || bits > maxScanPrefixBits {
		return nil
	}

	numHosts := 1 << (32 - bits)

	ips := make([]netip.Addr, 0, numHosts-2)
	// Skip network address (i=0) and broadcast address (i=numHosts-1)
	for i := 1; i < numHosts-1; i++ {
		ips = append(ips, addToIP(cidr.Addr(), i))
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
