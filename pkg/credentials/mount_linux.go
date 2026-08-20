//go:build linux

package credentials

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// ReadCAFromStatePartition reads the machine CA from the STATE partition.
// It tries Talos's own /system/state mount first (no raw block device mount
// needed), then falls back to mounting the raw block device (with LUKS2
// fallback) for environments where /system/state isn't available or doesn't
// contain config.yaml.
func ReadCAFromStatePartition() (*MachineConfigCA, error) {
	// Fast path: Talos mounts the STATE partition at /system/state.
	// Try reading config.yaml there first — no mount syscalls needed.
	if ca, err := readConfigFromPath(SystemStateMountPath); err == nil {
		return ca, nil
	}

	// Fallback: mount the raw STATE partition under /run/autobootstrap.
	if err := os.MkdirAll(MountBasePath, 0700); err != nil {
		return nil, fmt.Errorf("failed to create mount base directory: %w", err)
	}

	mountPoint, err := os.MkdirTemp(MountBasePath, "state-partition-")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp mount point: %w", err)
	}
	defer func() { _ = os.RemoveAll(mountPoint) }()

	if rawErr := mountPartition(StatePartitionPath, mountPoint); rawErr != nil {
		if encErr := mountPartition(StatePartitionEncryptedPath, mountPoint); encErr != nil {
			return nil, fmt.Errorf("failed to mount STATE partition (tried %s: %v, and %s: %v)",
				StatePartitionPath, rawErr, StatePartitionEncryptedPath, encErr)
		}
	}
	defer func() { _ = unmountPartition(mountPoint) }()

	ca, err := readConfigFromPath(mountPoint)
	if err != nil {
		return nil, fmt.Errorf("failed to read machine CA: %w", err)
	}
	return ca, nil
}

const (
	// MountBasePath is the base directory for temporary mount operations.
	MountBasePath = "/run/autobootstrap"
)

// mountPartition mounts a partition at the specified mount point.
func mountPartition(device, mountPoint string) error {
	err := unix.Mount(device, mountPoint, "xfs", unix.MS_RDONLY, "")
	if err != nil {
		return fmt.Errorf("mount syscall failed: %w", err)
	}
	return nil
}

// unmountPartition unmounts a partition.
func unmountPartition(mountPoint string) error {
	err := unix.Unmount(mountPoint, 0)
	if err != nil {
		return fmt.Errorf("unmount syscall failed: %w", err)
	}
	return nil
}
