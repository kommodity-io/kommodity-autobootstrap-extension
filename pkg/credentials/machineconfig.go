package credentials

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const (
	// StatePartitionPath is the path to the STATE partition block device.
	StatePartitionPath = "/dev/disk/by-partlabel/STATE"

	// StatePartitionEncryptedPath is the device mapper path for the LUKS2-encrypted STATE partition.
	StatePartitionEncryptedPath = "/dev/mapper/luks2-STATE"

	// ConfigFileName is the name of the machine config file on the STATE partition.
	ConfigFileName = "config.yaml"

	// SystemStateMountPath is where Talos mounts the STATE partition in the
	// root filesystem.
	SystemStateMountPath = "/system/state"
)

// MachineConfigCA contains the CA certificate and key from the machine config.
type MachineConfigCA struct {
	Crt string // Base64-encoded certificate
	Key string // Base64-encoded private key
}

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

// readConfigFromPath reads config.yaml from the given directory.
func readConfigFromPath(dir string) (*MachineConfigCA, error) {
	configPath := filepath.Join(dir, ConfigFileName)
	configData, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %q: %w", configPath, err)
	}
	return parseConfigForCA(configData)
}
