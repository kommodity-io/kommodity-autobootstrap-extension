package bootstrap

import (
	"context"
	"time"

	talosclient "github.com/siderolabs/talos/pkg/machinery/client"
	machineapi "github.com/siderolabs/talos/pkg/machinery/api/machine"
)

// IsClusterBootstrapped checks if the cluster has already been bootstrapped
// by checking for etcd members. Uses a short timeout to avoid blocking
// when etcd is not yet running.
func IsClusterBootstrapped(ctx context.Context, client *talosclient.Client) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	members, err := client.EtcdMemberList(ctx, &machineapi.EtcdMemberListRequest{})
	if err != nil {
		// etcd not running = not bootstrapped
		return false, nil
	}

	if len(members.Messages) > 0 && len(members.Messages[0].Members) > 0 {
		return true, nil
	}

	return false, nil
}

// WaitForEtcdReady waits for etcd to become ready after bootstrap.
func WaitForEtcdReady(ctx context.Context, client *talosclient.Client,
	timeout time.Duration) error {

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			members, err := client.EtcdMemberList(ctx, &machineapi.EtcdMemberListRequest{})
			if err != nil {
				continue
			}
			if len(members.Messages) > 0 && len(members.Messages[0].Members) > 0 {
				return nil
			}
		}
	}
}
