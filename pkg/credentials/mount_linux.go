//go:build linux

package credentials

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

const (
	// MountBasePath is the base directory for temporary mount operations.
	// This aligns with the /var/mnt volume mounted in the container configuration.
	MountBasePath = "/var/mnt/autobootstrap"
)

// ReadCAFromStatePartition reads the machine CA from the STATE partition.
// It mounts the partition temporarily, reads the config, and extracts the CA.
func ReadCAFromStatePartition() (*MachineConfigCA, error) {
	// Ensure the base mount directory exists
	if err := os.MkdirAll(MountBasePath, 0700); err != nil {
		return nil, fmt.Errorf("failed to create mount base directory: %w", err)
	}

	// Create a temporary mount point under our dedicated directory
	mountPoint, err := os.MkdirTemp(MountBasePath, "state-partition-")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp mount point: %w", err)
	}
	defer os.RemoveAll(mountPoint)

	// Mount the STATE partition
	if err := mountPartition(StatePartitionPath, mountPoint); err != nil {
		return nil, fmt.Errorf("failed to mount STATE partition: %w", err)
	}
	defer unmountPartition(mountPoint)

	// Read the config file
	configPath := filepath.Join(mountPoint, ConfigFileName)
	configData, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	return parseConfigForCA(configData)
}

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
