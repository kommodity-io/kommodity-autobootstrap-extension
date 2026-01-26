package credentials

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
