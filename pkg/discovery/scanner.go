package discovery

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/netip"
	"sync"
	"time"

	"github.com/cosi-project/runtime/pkg/resource"
	"github.com/cosi-project/runtime/pkg/safe"
	talosclient "github.com/siderolabs/talos/pkg/machinery/client"
	configres "github.com/siderolabs/talos/pkg/machinery/resources/config"
	runtimeres "github.com/siderolabs/talos/pkg/machinery/resources/runtime"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

const (
	// TalosAPIPort is the default port for Talos API.
	TalosAPIPort = 50000
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
func ScanCIDRForTalosNodes(ctx context.Context, cidr netip.Prefix,
	localIP netip.Addr, timeout time.Duration, concurrency int) ([]DiscoveredNode, error) {

	var (
		nodes   []DiscoveredNode
		nodesMu sync.Mutex
	)

	ips := GenerateIPsInCIDR(cidr)

	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(concurrency)

	for _, ip := range ips {
		// Skip local IP
		if ip == localIP {
			continue
		}

		ip := ip // capture for goroutine
		g.Go(func() error {
			node, err := probeTalosNode(ctx, ip, timeout)
			if err != nil {
				return nil // Not a Talos node or unreachable, skip silently
			}

			nodesMu.Lock()
			nodes = append(nodes, *node)
			nodesMu.Unlock()
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	return nodes, nil
}

// probeTalosNode attempts to connect to a potential Talos node and retrieve its info.
func probeTalosNode(ctx context.Context, ip netip.Addr, timeout time.Duration) (*DiscoveredNode, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	endpoint := fmt.Sprintf("%s:%d", ip.String(), TalosAPIPort)

	// Create client with insecure TLS (required for discovery of unknown nodes)
	client, err := talosclient.New(ctx,
		talosclient.WithEndpoints(endpoint),
		talosclient.WithGRPCDialOptions(
			grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
				InsecureSkipVerify: true,
			})),
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

	// Get boot time from MachineStatus resource
	bootTime := time.Now()

	machineStatus, err := safe.StateGet[*runtimeres.MachineStatus](nodeCtx, client.COSI,
		resource.NewMetadata(runtimeres.NamespaceName, runtimeres.MachineStatusType,
			runtimeres.MachineStatusID, resource.VersionUndefined))
	if err == nil && machineStatus.TypedSpec().Stage != runtimeres.MachineStageUnknown {
		// Use version info if available
		if len(version.Messages) > 0 && version.Messages[0].Version != nil {
			// Parse built time as a proxy for consistent ordering
			builtStr := version.Messages[0].Version.Built
			if builtStr != "" {
				parsed, err := time.Parse(time.RFC3339, builtStr)
				if err == nil {
					bootTime = parsed
				}
			}
		}
	}

	return &DiscoveredNode{
		IP:             ip,
		IsControlPlane: mt.MachineType().String() == "controlplane",
		CreationTime:   bootTime,
		Hostname:       hostname,
	}, nil
}

// GetLocalNodeInfo retrieves information about the local node.
func GetLocalNodeInfo(ctx context.Context, client *talosclient.Client,
	localIP netip.Addr) (*DiscoveredNode, error) {

	version, err := client.Version(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get version: %w", err)
	}

	var hostname string
	if len(version.Messages) > 0 && version.Messages[0].Metadata != nil {
		hostname = version.Messages[0].Metadata.Hostname
	}

	// Get boot time from MachineStatus
	bootTime := time.Now() // fallback
	machineStatus, err := safe.StateGet[*runtimeres.MachineStatus](ctx, client.COSI,
		resource.NewMetadata(runtimeres.NamespaceName, runtimeres.MachineStatusType,
			runtimeres.MachineStatusID, resource.VersionUndefined))
	if err == nil {
		// MachineStatus gives us uptime info indirectly
		_ = machineStatus // use if we can extract boot time
	}

	return &DiscoveredNode{
		IP:             localIP,
		IsControlPlane: true, // We only call this on control plane nodes
		CreationTime:   bootTime,
		Hostname:       hostname,
	}, nil
}
