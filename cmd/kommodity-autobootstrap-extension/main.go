package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kommodity-io/kommodity/pkg/logging"
	talosclient "github.com/siderolabs/talos/pkg/machinery/client"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/kommodity/talos-auto-bootstrap/internal/config"
	"github.com/kommodity/talos-auto-bootstrap/pkg/bootstrap"
	"github.com/kommodity/talos-auto-bootstrap/pkg/discovery"
	"github.com/kommodity/talos-auto-bootstrap/pkg/election"
)

// Version is set at build time.
var Version = "dev"

const (
	// MachineSocket is the path to the Talos machined Unix socket.
	// We connect to machined directly for gRPC calls (Bootstrap, EtcdMemberList).
	// Note: COSI access via machined is denied for extensions, so we use
	// filesystem-based checks instead of COSI queries.
	MachineSocket = "/system/run/machined/machine.sock"

	// EtcdSecretsPath is the path to etcd secrets directory.
	// This directory only exists on control plane nodes.
	EtcdSecretsPath = "/system/secrets/etcd"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	logger := logging.NewLogger()
	zap.ReplaceGlobals(logger)

	zap.L().Info("starting talos-auto-bootstrap", zap.String("version", Version))

	cfg, err := config.Load()
	if err != nil {
		zap.L().Fatal("failed to load config", zap.Error(err))
	}

	if err := run(ctx, cfg); err != nil {
		zap.L().Fatal("bootstrap service failed", zap.Error(err))
	}

	zap.L().Info("bootstrap service completed successfully")
	os.Exit(0)
}

func run(ctx context.Context, cfg *config.Config) error {
	// Check if this is a control plane node using filesystem
	// (etcd secrets directory only exists on control plane nodes)
	if !isControlPlane() {
		zap.L().Info("worker node detected (no etcd secrets), exiting")
		return nil
	}

	zap.L().Info("control plane node detected, starting bootstrap process")

	// Wait for machined socket with retries
	client, err := waitForMachined(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	// Check if cluster is already bootstrapped
	bootstrapped, err := bootstrap.IsClusterBootstrapped(ctx, client)
	if err == nil && bootstrapped {
		zap.L().Info("cluster already bootstrapped, exiting")
		return nil
	}

	return runBootstrapLoop(ctx, client, cfg)
}

// waitForMachined waits for the machined socket to become available.
func waitForMachined(ctx context.Context) (*talosclient.Client, error) {
	for {
		client, err := talosclient.New(ctx,
			talosclient.WithUnixSocket(MachineSocket),
			talosclient.WithGRPCDialOptions(
				grpc.WithTransportCredentials(insecure.NewCredentials()),
			),
		)
		if err == nil {
			zap.L().Info("connected to machined")
			return client, nil
		}

		zap.L().Info("waiting for machined socket", zap.String("path", MachineSocket), zap.Error(err))

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

// isControlPlane checks if the current node is a control plane node.
// It uses a filesystem-based check: the etcd secrets directory only exists
// on control plane nodes.
func isControlPlane() bool {
	_, err := os.Stat(EtcdSecretsPath)
	return err == nil
}

// runBootstrapLoop is the main loop that handles discovery, election, and bootstrap.
func runBootstrapLoop(ctx context.Context, client *talosclient.Client, cfg *config.Config) error {
	backoff := 5 * time.Second
	coordinator := bootstrap.NewCoordinator(client, cfg.PreBootstrapDelay)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Check if cluster is already bootstrapped
		bootstrapped, err := bootstrap.IsClusterBootstrapped(ctx, client)
		if err == nil && bootstrapped {
			zap.L().Info("cluster already bootstrapped")
			return nil
		}

		// Get network information using filesystem/net package
		// (COSI access is not available to extensions)
		netInfo, err := discovery.GetNetworkInfo()
		if err != nil {
			zap.L().Warn("failed to get network info, retrying", zap.Error(err))
			time.Sleep(backoff)
			continue
		}

		zap.L().Info("network discovered",
			zap.String("localIP", netInfo.LocalIP.String()),
			zap.String("cidr", netInfo.CIDR.String()),
			zap.String("gateway", netInfo.Gateway.String()))

		// Scan CIDR for peer Talos nodes
		peers, err := discovery.ScanCIDRForTalosNodes(ctx, netInfo.CIDR,
			netInfo.LocalIP, cfg.ScanTimeout, cfg.ScanConcurrency)
		if err != nil {
			zap.L().Warn("network scan failed, retrying", zap.Error(err))
			time.Sleep(backoff)
			continue
		}

		zap.L().Info("peer discovery complete", zap.Int("peers_found", len(peers)))
		for _, peer := range peers {
			zap.L().Debug("discovered peer",
				zap.String("ip", peer.IP.String()),
				zap.String("hostname", peer.Hostname),
				zap.Bool("controlplane", peer.IsControlPlane))
		}

		// Get local node information
		localNode, err := discovery.GetLocalNodeInfo(ctx, client, netInfo.LocalIP)
		if err != nil {
			zap.L().Warn("failed to get local node info, retrying", zap.Error(err))
			time.Sleep(backoff)
			continue
		}

		// Check if quorum is reached
		allNodes := append(peers, *localNode)
		if !election.QuorumReached(allNodes, cfg.QuorumNodes) {
			zap.L().Info("quorum not reached, waiting",
				zap.Int("found", len(peers)+1),
				zap.Int("required", cfg.QuorumNodes))
			time.Sleep(cfg.ScanInterval)
			continue
		}

		// Perform leader election
		result := election.ElectLeader(*localNode, peers)
		zap.L().Info("leader election complete",
			zap.String("leader", result.Leader.IP.String()),
			zap.String("leader_hostname", result.Leader.Hostname),
			zap.Bool("is_leader", result.IsLeader),
			zap.Int("candidates", len(result.Candidates)))

		if !result.IsLeader {
			zap.L().Info("not elected as leader, waiting for bootstrap")
			time.Sleep(cfg.FollowerCheckInterval)
			continue
		}

		// This node is the leader - execute bootstrap
		zap.L().Info("elected as leader, initiating bootstrap")
		err = coordinator.SafeBootstrap(ctx)
		if err != nil {
			zap.L().Error("bootstrap failed, retrying", zap.Error(err))
			time.Sleep(backoff)
			// Exponential backoff with cap
			backoff = min(backoff*2, cfg.MaxBackoff)
			continue
		}

		zap.L().Info("bootstrap successful")
		return nil
	}
}
