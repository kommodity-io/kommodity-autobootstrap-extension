package discovery

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cosi-project/runtime/pkg/resource"
	"github.com/cosi-project/runtime/pkg/safe"
	talosclient "github.com/siderolabs/talos/pkg/machinery/client"
	configres "github.com/siderolabs/talos/pkg/machinery/resources/config"
	"github.com/siderolabs/talos/pkg/machinery/resources/network"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/protobuf/types/known/emptypb"
)

const (
	// TalosAPIPort is the default port for Talos API.
	TalosAPIPort = 50000

	// minScanPrefixBits is the widest network to scan. A /16 is 65534 probes.
	minScanPrefixBits = 16

	// maxScanPrefixBits is the narrowest network with a usable host range.
	// A /31 or /32 contains no other hosts to discover.
	maxScanPrefixBits = 30
)

// DiscoveredNode represents a Talos node found during network scanning.
type DiscoveredNode struct {
	// IP is the node's IP address
	IP netip.Addr
	// IsControlPlane indicates if this is a control plane node
	IsControlPlane bool
	// CreationTime is the node's boot time (used for leader election)
	CreationTime time.Time
	// Hostname is the node's hostname
	Hostname string
}

// ScanCIDRForTalosNodes scans a CIDR range for Talos nodes.
// It probes each IP address in the range concurrently.
//
// clientTLS carries the client certificate used to authenticate to peers. apid
// requires client-cert auth, so a probe without one connects and is then
// rejected on the RPC, which is indistinguishable from an empty address.
//
// The whole range is swept before returning, and probes to empty addresses cost
// the full timeout, so a /16 takes minutes. That cost buys a candidate set that
// depends only on which nodes are reachable: election sorts over the set it is
// given, so two nodes electing different leaders and both bootstrapping is
// avoidable only where they agree on the set.
func ScanCIDRForTalosNodes(ctx context.Context, cidr netip.Prefix,
	localIP netip.Addr, timeout time.Duration, concurrency int,
	clientTLS *tls.Config) ([]DiscoveredNode, error) {

	var (
		nodes   []DiscoveredNode
		nodesMu sync.Mutex
	)

	// A /31 or /32 target means the CIDR came from a point-to-point or
	// single-host interface address. Scanning it finds no peers, so every node
	// would elect itself leader and bootstrap a separate etcd cluster. Refuse:
	// a failed bootstrap retries, a split one does not.
	if cidr.Bits() > maxScanPrefixBits {
		return nil, fmt.Errorf("scan CIDR %s has no usable host range; "+
			"set TALOS_AUTO_BOOTSTRAP_SCAN_CIDR to the node network", cidr)
	}

	if cidr.Bits() < minScanPrefixBits {
		return nil, fmt.Errorf("scan CIDR %s is too large to scan; "+
			"set TALOS_AUTO_BOOTSTRAP_SCAN_CIDR to a /16 or narrower", cidr)
	}

	ips := GenerateIPsInCIDR(cidr)
	// Never proceed on an empty scan: it is indistinguishable from having
	// scanned the network and found no peers.
	if len(ips) == 0 {
		return nil, fmt.Errorf("scan CIDR %s yielded no addresses to probe", cidr)
	}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(concurrency)

	for _, ip := range ips {
		// Skip local IP
		if ip == localIP {
			continue
		}

		if gctx.Err() != nil {
			break
		}

		ip := ip // capture for goroutine
		g.Go(func() error {
			node, err := probeTalosNode(gctx, ip, timeout, clientTLS)
			if err != nil {
				return nil // Not a Talos node or unreachable, skip silently
			}

			nodesMu.Lock()
			defer nodesMu.Unlock()

			nodes = append(nodes, *node)

			return nil
		})
	}

	// Errors here are cancellation of ctx, not probe failures: probes never
	// return one.
	_ = g.Wait()

	nodesMu.Lock()
	defer nodesMu.Unlock()

	return nodes, nil
}

// probeTalosNode attempts to connect to a potential Talos node and retrieve its info.
//
// clientTLS is used as given, so the peer is verified against the machine CA and
// its certificate must name the address dialled. Anything else answering on the
// port fails the handshake and is never reported as a peer. It must carry a CA:
// probing without one is refused rather than downgraded to an unverified dial.
func probeTalosNode(ctx context.Context, ip netip.Addr, timeout time.Duration,
	clientTLS *tls.Config) (*DiscoveredNode, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	endpoint := fmt.Sprintf("%s:%d", ip.String(), TalosAPIPort)

	// Talos signs each node's apid certificate with the machine CA and puts the
	// node's own addresses in its SANs, so verifying against clientTLS's CA and
	// dialling by IP both succeed with no special handling. waitForApid relies
	// on the same match for the local node.
	//
	// Refuse to probe without a CA rather than falling back to an unverified
	// dial: nothing would then distinguish a peer from anything else answering
	// on the port, which is the case this verification exists to reject.
	if clientTLS == nil || clientTLS.RootCAs == nil {
		return nil, errors.New("no machine CA to verify peers against")
	}

	client, err := talosclient.New(ctx,
		talosclient.WithEndpoints(endpoint),
		talosclient.WithTLSConfig(clientTLS),
		talosclient.WithGRPCDialOptions(
			grpc.WithTransportCredentials(credentials.NewTLS(clientTLS)),
		),
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = client.Close() }()

	nodeCtx := talosclient.WithNode(ctx, ip.String())

	// Verify it's a Talos node by getting version
	version, err := client.Version(nodeCtx)
	if err != nil {
		return nil, err
	}

	// Get machine type to determine if control plane
	mt, err := safe.StateGet[*configres.MachineType](nodeCtx, client.COSI,
		resource.NewMetadata(configres.NamespaceName, configres.MachineTypeType,
			configres.MachineTypeID, resource.VersionUndefined))
	if err != nil {
		return nil, err
	}

	var hostname string
	if len(version.Messages) > 0 && version.Messages[0].Metadata != nil {
		hostname = version.Messages[0].Metadata.Hostname
	}

	// apid does not always populate hostname in the response metadata, which
	// leaves a discovered peer nameless in the election logs. The local node
	// falls back to /etc/hostname; for a peer, ask the node itself. Non-fatal:
	// the election compares IPs, so a missing hostname only costs readability.
	if hostname == "" {
		hostnameStatus, err := safe.StateGet[*network.HostnameStatus](nodeCtx, client.COSI,
			resource.NewMetadata(network.NamespaceName, network.HostnameStatusType,
				network.HostnameID, resource.VersionUndefined))
		if err == nil {
			hostname = hostnameStatus.TypedSpec().Hostname
		}
	}

	// SystemStat.BootTime is the peer's boot time, comparable to the local
	// node's getBootTime(). Version.Built is not a substitute: it is the image
	// build date and is identical on every node running the same Talos release.
	//
	// Keep it non-fatal even though the probe is authenticated: a denial or a
	// transient failure here would otherwise drop a peer and leave the node
	// believing it is alone, which is what the scan guards above exist to prevent.
	bootTime := time.Now()

	if stat, err := client.MachineClient.SystemStat(nodeCtx, &emptypb.Empty{}); err == nil &&
		len(stat.Messages) > 0 && stat.Messages[0].BootTime > 0 {
		bootTime = time.Unix(int64(stat.Messages[0].BootTime), 0)
	}

	return &DiscoveredNode{
		IP:             ip,
		IsControlPlane: mt.MachineType().String() == "controlplane",
		CreationTime:   bootTime,
		Hostname:       hostname,
	}, nil
}

// GetLocalNodeInfo retrieves information about the local node.
// Uses gRPC Version() call and filesystem instead of COSI.
func GetLocalNodeInfo(ctx context.Context, client *talosclient.Client,
	localIP netip.Addr) (*DiscoveredNode, error) {

	var hostname string
	var bootTime time.Time

	// Try to get hostname from Version() gRPC call
	version, err := client.Version(ctx)
	if err == nil && len(version.Messages) > 0 && version.Messages[0].Metadata != nil {
		hostname = version.Messages[0].Metadata.Hostname
	}

	// Fallback: get hostname from /etc/hostname or os.Hostname()
	if hostname == "" {
		if data, err := os.ReadFile("/etc/hostname"); err == nil {
			hostname = strings.TrimSpace(string(data))
		} else if h, err := os.Hostname(); err == nil {
			hostname = h
		}
	}

	// Get boot time from /proc/stat
	bootTime = getBootTime()

	return &DiscoveredNode{
		IP:             localIP,
		IsControlPlane: true, // We only call this on control plane nodes
		CreationTime:   bootTime,
		Hostname:       hostname,
	}, nil
}

// getBootTime reads the system boot time from /proc/stat.
// Uses /host/proc when running in container to avoid conflicting with container's /proc.
func getBootTime() time.Time {
	// Try /host/proc first (container environment), then /proc (native)
	data, err := os.ReadFile("/host/proc/stat")
	if err != nil {
		data, err = os.ReadFile("/proc/stat")
	}
	if err != nil {
		return time.Now()
	}

	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "btime ") {
			var btime int64
			if _, err := fmt.Sscanf(line, "btime %d", &btime); err == nil {
				return time.Unix(btime, 0)
			}
		}
	}

	return time.Now()
}
