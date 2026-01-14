package bootstrap

import (
	"context"
	"time"

	machineapi "github.com/siderolabs/talos/pkg/machinery/api/machine"
	talosclient "github.com/siderolabs/talos/pkg/machinery/client"
	"go.uber.org/zap"
)

// Coordinator handles the safe execution of cluster bootstrap.
type Coordinator struct {
	client            *talosclient.Client
	preBootstrapDelay time.Duration
}

// NewCoordinator creates a new bootstrap coordinator.
func NewCoordinator(client *talosclient.Client, preBootstrapDelay time.Duration) *Coordinator {
	return &Coordinator{
		client:            client,
		preBootstrapDelay: preBootstrapDelay,
	}
}

// SafeBootstrap executes the bootstrap process with safety checks.
// It includes a pre-bootstrap delay to allow other nodes to catch up,
// and performs a final check before executing bootstrap.
func (c *Coordinator) SafeBootstrap(ctx context.Context) error {
	// Pre-bootstrap delay - allows other nodes time to participate in election
	zap.L().Info("waiting before bootstrap", zap.Duration("delay", c.preBootstrapDelay))

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(c.preBootstrapDelay):
	}

	// Final check - another node may have bootstrapped during our delay
	bootstrapped, _ := IsClusterBootstrapped(ctx, c.client)
	if bootstrapped {
		zap.L().Info("cluster was bootstrapped by another node")
		return nil
	}

	// Execute bootstrap
	zap.L().Info("executing bootstrap")
	err := c.client.Bootstrap(ctx, &machineapi.BootstrapRequest{
		RecoverEtcd: false,
	})
	if err != nil {
		return err
	}

	// Wait for etcd to become ready
	zap.L().Info("waiting for etcd to become ready")
	return WaitForEtcdReady(ctx, c.client, 5*time.Minute)
}
