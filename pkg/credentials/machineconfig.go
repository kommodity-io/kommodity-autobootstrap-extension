package credentials

const (
	// StatePartitionPath is the path to the STATE partition block device.
	StatePartitionPath = "/dev/disk/by-partlabel/STATE"

	// StatePartitionEncryptedPath is the device mapper path for the LUKS2-encrypted STATE partition.
	// When KMS disk encryption is enabled, Talos opens the LUKS container and maps the decrypted
	// device to /dev/mapper/luks2-STATE.
	StatePartitionEncryptedPath = "/dev/mapper/luks2-STATE"

	// ConfigFileName is the name of the machine config file on the STATE partition.
	ConfigFileName = "config.yaml"
)

// MachineConfigCA contains the CA certificate and key from the machine config.
type MachineConfigCA struct {
	Crt string // Base64-encoded certificate
	Key string // Base64-encoded private key
}
