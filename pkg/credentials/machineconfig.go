package credentials

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

const (
	// StatePartitionPath is the path to the STATE partition block device.
	StatePartitionPath = "/dev/disk/by-partlabel/STATE"

	// ConfigFileName is the name of the machine config file on the STATE partition.
	ConfigFileName = "config.yaml"
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
