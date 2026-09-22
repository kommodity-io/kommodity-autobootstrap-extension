package bootstrap

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/netip"
	"time"

	machineapi "github.com/siderolabs/talos/pkg/machinery/api/machine"
	talosclient "github.com/siderolabs/talos/pkg/machinery/client"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// Coordinator handles the safe execution of cluster bootstrap.
type Coordinator struct {
	client            *talosclient.Client
	preBootstrapDelay time.Duration
	tlsConfig         *tls.Config
}

// NewCoordinator creates a new bootstrap coordinator.
func NewCoordinator(client *talosclient.Client, preBootstrapDelay time.Duration, tlsConfig *tls.Config) *Coordinator {
	return &Coordinator{
		client:            client,
		preBootstrapDelay: preBootstrapDelay,
		tlsConfig:         tlsConfig,
	}
}

// IsPeerBootstrapped checks if any peer node has etcd running.
// Connects to each peer's Talos API and calls EtcdMemberList.
// If any peer has etcd members, the cluster is already bootstrapped.
func IsPeerBootstrapped(ctx context.Context, peers []netip.Addr, tlsConfig *tls.Config) bool {
	for _, peer := range peers {
		endpoint := fmt.Sprintf("%s:50000", peer.String())
		peerCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		client, err := talosclient.New(peerCtx,
			talosclient.WithEndpoints(endpoint),
			talosclient.WithTLSConfig(tlsConfig),
			talosclient.WithGRPCDialOptions(
				grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)),
			),
		)
		if err != nil {
			cancel()
			continue
		}
		nodeCtx := talosclient.WithNode(peerCtx, peer.String())
		members, err := client.EtcdMemberList(nodeCtx, &machineapi.EtcdMemberListRequest{})
		_ = client.Close()
		cancel()
		if err != nil {
			continue
		}
		if len(members.Messages) > 0 && len(members.Messages[0].Members) > 0 {
			zap.L().Info("peer has etcd running, cluster already bootstrapped",
				zap.String("peer", peer.String()))
			return true
		}
	}
	return false
}

// SafeBootstrap executes the bootstrap process with safety checks.
// It includes a pre-bootstrap delay to allow other nodes to catch up,
// and performs final checks (both local and peer) before executing bootstrap.
func (c *Coordinator) SafeBootstrap(ctx context.Context, peers []netip.Addr) error {
	// Pre-bootstrap delay - allows other nodes time to participate in election
	zap.L().Info("waiting before bootstrap", zap.Duration("delay", c.preBootstrapDelay))

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(c.preBootstrapDelay):
	}

	// Final local check - another node may have bootstrapped during our delay
	bootstrapped, _ := IsClusterBootstrapped(ctx, c.client)
	if bootstrapped {
		zap.L().Info("cluster was bootstrapped by another node (local check)")
		return nil
	}

	// Check peers - any peer with etcd running means the cluster exists
	if IsPeerBootstrapped(ctx, peers, c.tlsConfig) {
		zap.L().Info("cluster was bootstrapped by a peer (peer check)")
		return nil
	}

	// Execute bootstrap
	zap.L().Info("executing bootstrap")
	err := c.client.Bootstrap(ctx, &machineapi.BootstrapRequest{
		RecoverEtcd: false,
	})
	if err != nil {
		return fmt.Errorf("bootstrap failed: %w", err)
	}

	// Wait for etcd to become ready
	zap.L().Info("waiting for etcd to become ready")
	return WaitForEtcdReady(ctx, c.client, 5*time.Minute)
}
