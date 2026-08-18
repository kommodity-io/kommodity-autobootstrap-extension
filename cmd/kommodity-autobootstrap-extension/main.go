package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kommodity-io/kommodity/pkg/logging"
	talosclient "github.com/siderolabs/talos/pkg/machinery/client"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/kommodity/talos-auto-bootstrap/internal/config"
	"github.com/kommodity/talos-auto-bootstrap/pkg/bootstrap"
	creds "github.com/kommodity/talos-auto-bootstrap/pkg/credentials"
	"github.com/kommodity/talos-auto-bootstrap/pkg/discovery"
	"github.com/kommodity/talos-auto-bootstrap/pkg/election"
)

// Version is set at build time.
var Version = "dev"

const (
	// ApidPort is the port where apid listens.
	// We connect to apid via TLS with an admin certificate for gRPC calls
	// (Bootstrap, EtcdMemberList). Direct machined socket access is denied
	// for extensions due to RBAC, so we use apid with generated admin credentials.
	ApidPort = "50000"

	// EtcdSecretsPath is the path to etcd secrets directory.
	// This directory only exists on control plane nodes.
	EtcdSecretsPath = "/system/secrets/etcd"

	// QuorumWarnAfter is how long quorum may go unmet before the wait is
	// reported at Warn. Long enough that an ordinary staggered boot stays quiet,
	// short enough to notice a stuck cluster.
	//
	// Measured in elapsed time rather than scan rounds: a round costs a full
	// sweep of the range when quorum is unmet, because the scan only returns
	// early once enough control planes answer. On a large range that is minutes
	// per round, so a round count would delay the warning by however long the
	// sweep happens to take.
	QuorumWarnAfter = 3 * time.Minute
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

	// Get network info first to determine local IP for apid connection.
	// apid's TLS certificate is issued for the node's IP, so we must connect
	// using the actual IP (not localhost) for certificate validation to pass.
	netInfo, err := discovery.GetNetworkInfo(cfg.ScanCIDR)
	if err != nil {
		return fmt.Errorf("failed to get network info: %w", err)
	}

	apidEndpoint := net.JoinHostPort(netInfo.LocalIP.String(), ApidPort)
	zap.L().Info("resolved apid endpoint", zap.String("endpoint", apidEndpoint))

	// Read machine CA from the STATE partition
	zap.L().Info("reading machine CA from STATE partition")
	machineCA, err := creds.ReadCAFromStatePartition()
	if err != nil {
		return fmt.Errorf("failed to read machine CA: %w", err)
	}

	// Generate TLS config with os:admin credentials from the machine CA
	zap.L().Info("generating admin TLS credentials from machine CA")
	tlsConfig, err := creds.GenerateTLSConfig(machineCA.Crt, machineCA.Key)
	if err != nil {
		return fmt.Errorf("failed to generate TLS config: %w", err)
	}

	// Wait for apid with TLS authentication
	client, err := waitForApid(ctx, tlsConfig, apidEndpoint)
	if err != nil {
		return fmt.Errorf("failed to connect to apid: %w", err)
	}
	defer func() { _ = client.Close() }()

	// Check if cluster is already bootstrapped
	bootstrapped, err := bootstrap.IsClusterBootstrapped(ctx, client)
	if err == nil && bootstrapped {
		zap.L().Info("cluster already bootstrapped, exiting")
		return nil
	}

	return runBootstrapLoop(ctx, client, cfg, tlsConfig)
}

// waitForApid waits for apid to become available and connects with TLS credentials.
func waitForApid(ctx context.Context, tlsConfig *tls.Config, endpoint string) (*talosclient.Client, error) {
	for {
		client, err := talosclient.New(ctx,
			talosclient.WithEndpoints(endpoint),
			talosclient.WithTLSConfig(tlsConfig),
			talosclient.WithGRPCDialOptions(
				grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)),
			),
		)
		if err == nil {
			zap.L().Info("connected to apid with admin credentials")
			return client, nil
		}

		zap.L().Info("waiting for apid", zap.String("endpoint", endpoint), zap.Error(err))

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

// logRetryFailure reports a failed round of the bootstrap loop, at Warn where a
// later round can resolve the condition and at Error where it cannot.
//
// The interface and its route both appear after this loop starts, so a node that
// has not finished configuring its network produces these on the first rounds and
// succeeds on a later one. Logging that at Error puts the level that means
// "someone has to act" on the ordinary path of every boot, which is how a level
// stops being read.
func logRetryFailure(msg string, err error) {
	if errors.Is(err, discovery.ErrNoNetwork) || errors.Is(err, discovery.ErrUnusableRange) {
		zap.L().Warn(msg, zap.Error(err))

		return
	}

	zap.L().Error(msg, zap.Error(err))
}

// trackUnmetQuorum reports how long quorum has gone unmet and whether that is
// now long enough to warn. unmetSince is the zero time when quorum was last met.
//
// Reaching quorum resets it rather than latching: a cluster whose peers appear
// late is the normal slow-boot path and should look normal once it resolves, and
// a node that dips in and out of quorum gets the full grace period again rather
// than warning on every subsequent dip.
func trackUnmetQuorum(unmetSince, now time.Time, quorumMet bool) (time.Time, bool) {
	if quorumMet {
		return time.Time{}, false
	}

	if unmetSince.IsZero() {
		unmetSince = now
	}

	return unmetSince, now.Sub(unmetSince) >= QuorumWarnAfter
}

// quorumCount reports the node count that quorum is judged on: control plane
// peers plus this node. Quorum ignores workers, so a diagnostic that counted
// them would contradict itself, reporting more nodes found than required while
// the node keeps waiting.
//
// The local node is always a control plane here: run() returns before the loop
// unless isControlPlane(), and GetLocalNodeInfo sets IsControlPlane on that
// basis.
//
// Separated from the log call to be testable, not to be reused. Inline, the
// count was unreachable from a test and shipped counting workers.
func quorumCount(controlPlanePeers int) int {
	return controlPlanePeers + 1
}

// runBootstrapLoop is the main loop that handles discovery, election, and bootstrap.
func runBootstrapLoop(ctx context.Context, client *talosclient.Client, cfg *config.Config,
	tlsConfig *tls.Config) error {
	backoff := 5 * time.Second
	coordinator := bootstrap.NewCoordinator(client, cfg.PreBootstrapDelay)

	// When quorum first went unmet, zero while it is being met.
	var unmetQuorumSince time.Time

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
		netInfo, err := discovery.GetNetworkInfo(cfg.ScanCIDR)
		if err != nil {
			logRetryFailure("failed to get network info, retrying", err)
			time.Sleep(backoff)
			continue
		}

		zap.L().Info("network discovered",
			zap.String("localIP", netInfo.LocalIP.String()),
			zap.String("cidr", netInfo.CIDR.String()),
			zap.String("gateway", netInfo.Gateway.String()))

		// Scan CIDR for peer Talos nodes
		peers, err := discovery.ScanCIDRForTalosNodes(ctx, netInfo.CIDR,
			netInfo.LocalIP, cfg.ScanTimeout, cfg.ScanConcurrency, tlsConfig)
		if err != nil {
			// Never proceed on a failed scan: electing from a candidate set of
			// one is what produces two clusters.
			logRetryFailure("network scan failed, not electing a leader", err)
			time.Sleep(backoff)
			continue
		}

		controlPlanePeers := 0

		for _, peer := range peers {
			if peer.IsControlPlane {
				controlPlanePeers++
			}
		}

		// peers_found counts every Talos node that answered, workers included.
		// Only control planes are candidates for quorum and election.
		zap.L().Info("peer discovery complete",
			zap.Int("peers_found", len(peers)),
			zap.Int("controlplane_peers", controlPlanePeers))
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
			var warn bool

			unmetQuorumSince, warn = trackUnmetQuorum(unmetQuorumSince, time.Now(), false)

			// Waiting for peers is legitimate and the node keeps waiting, but
			// waiting silently forever is indistinguishable from a hang. Say so
			// once the wait is long enough to be worth an operator's attention.
			if warn {
				zap.L().Warn("quorum still not reached, continuing to wait",
					zap.Int("found", quorumCount(controlPlanePeers)),
					zap.Int("required", cfg.QuorumNodes),
					zap.Duration("waiting", time.Since(unmetQuorumSince)))
			} else {
				zap.L().Info("quorum not reached, waiting",
					zap.Int("found", quorumCount(controlPlanePeers)),
					zap.Int("required", cfg.QuorumNodes))
			}

			time.Sleep(cfg.ScanInterval)

			continue
		}

		unmetQuorumSince, _ = trackUnmetQuorum(unmetQuorumSince, time.Now(), true)

		// QuorumNodes=1 lets a node that has not yet seen its peers elect
		// itself, so two nodes can bootstrap separately. Discovering other
		// control planes proves the cluster is not single node. The operator's
		// value is honoured: they may be staging a bring-up, and this condition
		// is timing-dependent, so refusing would make the same configuration
		// succeed or fail from one boot to the next.
		if cfg.QuorumNodes == 1 && controlPlanePeers > 0 {
			zap.L().Warn("QUORUM_NODES=1 with other control planes discovered; "+
				"nodes may bootstrap separately, set it to the control plane count",
				zap.Int("controlplane_peers", controlPlanePeers))
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
