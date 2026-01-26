package config

import (
	"time"

	"github.com/kelseyhightower/envconfig"
)

// Config holds runtime configuration for the auto-bootstrap service.
type Config struct {
	// ScanInterval is the time between network discovery scans
	ScanInterval time.Duration `envconfig:"TALOS_AUTO_BOOTSTRAP_SCAN_INTERVAL" default:"30s"`

	// FollowerCheckInterval is how often followers check bootstrap status
	FollowerCheckInterval time.Duration `envconfig:"TALOS_AUTO_BOOTSTRAP_FOLLOWER_CHECK_INTERVAL" default:"15s"`

	// QuorumNodes is the expected number of control plane nodes required for quorum
	QuorumNodes int `envconfig:"TALOS_AUTO_BOOTSTRAP_QUORUM_NODES" default:"1"`

	// PreBootstrapDelay is the wait time before leader executes bootstrap
	PreBootstrapDelay time.Duration `envconfig:"TALOS_AUTO_BOOTSTRAP_PRE_BOOTSTRAP_DELAY" default:"10s"`

	// MaxBackoff is the maximum retry backoff duration
	MaxBackoff time.Duration `envconfig:"TALOS_AUTO_BOOTSTRAP_MAX_BACKOFF" default:"2m"`

	// ScanTimeout is the timeout for probing each node during discovery
	ScanTimeout time.Duration `envconfig:"TALOS_AUTO_BOOTSTRAP_SCAN_TIMEOUT" default:"2s"`

	// ScanConcurrency is the maximum number of concurrent node probes
	ScanConcurrency int `envconfig:"TALOS_AUTO_BOOTSTRAP_SCAN_CONCURRENCY" default:"50"`
}

// Load reads configuration from environment variables.
func Load() (*Config, error) {
	var cfg Config
	err := envconfig.Process("", &cfg)
	return &cfg, err
}
