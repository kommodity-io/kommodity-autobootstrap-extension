//go:build linux

package credentials

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
	"gopkg.in/yaml.v3"
)

// machineConfig represents the relevant parts of the Talos machine config.
type machineConfig struct {
	Machine struct {
		CA struct {
			Crt string `yaml:"crt"`
			Key string `yaml:"key"`
		} `yaml:"ca"`
	} `yaml:"machine"`
}

// parseConfigForCA parses machine config YAML and extracts the CA.
func parseConfigForCA(configData []byte) (*MachineConfigCA, error) {
	var config machineConfig
	if err := yaml.Unmarshal(configData, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	if config.Machine.CA.Crt == "" || config.Machine.CA.Key == "" {
		return nil, fmt.Errorf("machine.ca.crt or machine.ca.key not found in config")
	}

	return &MachineConfigCA{
		Crt: config.Machine.CA.Crt,
		Key: config.Machine.CA.Key,
	}, nil
}

const (
	// MountBasePath is the base directory for temporary mount operations.
	// Uses /run which is a writable tmpfs in Talos Linux.
	MountBasePath = "/run/autobootstrap"
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
	defer func() { _ = os.RemoveAll(mountPoint) }()

	// Mount the STATE partition.
	// Try the raw partition first (unencrypted), then fall back to the
	// device mapper path used when KMS disk encryption is enabled.
	if err := mountPartition(StatePartitionPath, mountPoint); err != nil {
		if err := mountPartition(StatePartitionEncryptedPath, mountPoint); err != nil {
			return nil, fmt.Errorf("failed to mount STATE partition (tried %s and %s): %w",
				StatePartitionPath, StatePartitionEncryptedPath, err)
		}
	}
	defer func() { _ = unmountPartition(mountPoint) }()

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
