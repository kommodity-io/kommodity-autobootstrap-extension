package main

import (
	"errors"
	"fmt"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/kommodity/talos-auto-bootstrap/pkg/discovery"
)

// A node configures its network after this loop starts, so the conditions that
// resolve on their own must not be reported at the level that means someone has
// to act. Anything else must keep it.
func TestLogRetryFailure(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want zapcore.Level
	}{
		{
			name: "interface not up yet",
			err:  discovery.ErrNoNetwork,
			want: zapcore.WarnLevel,
		},
		{
			name: "route not yet present",
			err:  discovery.ErrUnusableRange,
			want: zapcore.WarnLevel,
		},
		{
			// The sentinels reach the caller wrapped, and the level must not
			// depend on how deeply.
			name: "wrapped transient condition",
			err:  fmt.Errorf("scan: %w", fmt.Errorf("range: %w", discovery.ErrUnusableRange)),
			want: zapcore.WarnLevel,
		},
		{
			// A typo in TALOS_AUTO_BOOTSTRAP_SCAN_CIDR never resolves itself.
			name: "operator error",
			err:  errors.New(`invalid TALOS_AUTO_BOOTSTRAP_SCAN_CIDR "10.0.0.0"`),
			want: zapcore.ErrorLevel,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			core, logs := observer.New(zapcore.DebugLevel)
			restore := zap.ReplaceGlobals(zap.New(core))

			defer restore()

			logRetryFailure("discovery failed", tt.err)

			if logs.Len() != 1 {
				t.Fatalf("expected exactly one entry, got %d", logs.Len())
			}

			if got := logs.All()[0].Level; got != tt.want {
				t.Errorf("expected %v, got %v", tt.want, got)
			}
		})
	}
}
