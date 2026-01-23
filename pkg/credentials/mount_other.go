//go:build !linux

package credentials

import "fmt"

// ReadCAFromStatePartition is not supported on non-Linux platforms.
func ReadCAFromStatePartition() (*MachineConfigCA, error) {
	return nil, fmt.Errorf("ReadCAFromStatePartition is only supported on Linux")
}
