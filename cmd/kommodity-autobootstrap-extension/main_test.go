package main

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/kommodity/talos-auto-bootstrap/pkg/discovery"
	"github.com/kommodity/talos-auto-bootstrap/pkg/election"
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

// The quorum warning is measured in elapsed time rather than scan rounds. A
// round costs a full sweep of the range while quorum is unmet, since the scan
// only returns early once enough control planes answer, so a round count delays
// the warning by however long the sweep takes.
func TestQuorumWarnUsesElapsedTime(t *testing.T) {
	base := time.Unix(0, 0)

	tests := []struct {
		name    string
		elapsed time.Duration
		want    bool
	}{
		{name: "quiet immediately", elapsed: 0, want: false},
		{name: "quiet before the threshold", elapsed: QuorumWarnAfter - time.Second, want: false},
		{name: "warns at the threshold", elapsed: QuorumWarnAfter, want: true},
		{name: "still warns well past it", elapsed: 10 * QuorumWarnAfter, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// First unmet round records the start and cannot warn.
			since, warn := trackUnmetQuorum(time.Time{}, base, false)
			if warn {
				t.Fatal("the first unmet round must not warn")
			}

			_, warn = trackUnmetQuorum(since, base.Add(tt.elapsed), false)
			if warn != tt.want {
				t.Errorf("after %s: expected warn=%v, got %v", tt.elapsed, tt.want, warn)
			}
		})
	}
}

// Two slow sweeps must warn where two fast ones do not: the elapsed time is what
// matters, not the number of rounds. This is the case a round counter got wrong.
func TestQuorumWarnIsIndependentOfRoundCount(t *testing.T) {
	base := time.Unix(0, 0)

	since, _ := trackUnmetQuorum(time.Time{}, base, false)

	// Two rounds, each a long sweep, so well past the threshold.
	_, warn := trackUnmetQuorum(since, base.Add(2*QuorumWarnAfter), false)
	if !warn {
		t.Error("two slow rounds past the threshold must warn")
	}

	// Many rounds, all fast, so still inside the grace period.
	since, _ = trackUnmetQuorum(time.Time{}, base, false)

	for i := 1; i <= 20; i++ {
		var w bool

		since, w = trackUnmetQuorum(since, base.Add(time.Duration(i)*time.Second), false)
		if w {
			t.Fatalf("round %d warned inside the grace period", i)
		}
	}
}

// The warning must not latch. A node that waits past the threshold, then reaches
// quorum, goes quiet and gets the full grace period again if quorum is later
// lost, so a flapping peer does not warn forever about a condition that clears.
func TestQuorumWarnDoesNotLatch(t *testing.T) {
	base := time.Unix(0, 0)

	// Wait past the threshold and warn.
	since, _ := trackUnmetQuorum(time.Time{}, base, false)

	since, warn := trackUnmetQuorum(since, base.Add(QuorumWarnAfter), false)
	if !warn {
		t.Fatal("expected a warning once the threshold is passed")
	}

	// Quorum is met: the counter clears and the warning stops.
	since, warn = trackUnmetQuorum(since, base.Add(QuorumWarnAfter), true)
	if warn {
		t.Error("a resolved wait must not warn")
	}

	if !since.IsZero() {
		t.Errorf("expected the timer to reset, got %v", since)
	}

	// A later dip starts a fresh grace period rather than warning at once.
	dipStart := base.Add(2 * QuorumWarnAfter)

	since, warn = trackUnmetQuorum(since, dipStart, false)
	if warn {
		t.Error("a fresh dip must not warn immediately")
	}

	_, warn = trackUnmetQuorum(since, dipStart.Add(QuorumWarnAfter-time.Second), false)
	if warn {
		t.Error("the dip must get the full grace period again")
	}
}

// The reported count must match what QuorumReached judges, which is control
// planes only. Hardware measured found:2 required:3 on 1 control plane and 1
// worker, where the governing number was 1: the diagnostic misstated the
// quantity it exists to explain.
func TestQuorumCountExcludesWorkers(t *testing.T) {
	tests := []struct {
		name              string
		controlPlanePeers int
		want              int
	}{
		{"alone", 0, 1},
		{"one control plane peer", 1, 2},
		{"quorum of three", 2, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := quorumCount(tt.controlPlanePeers); got != tt.want {
				t.Errorf("quorumCount(%d) = %d, want %d",
					tt.controlPlanePeers, got, tt.want)
			}
		})
	}
}

// The reported count and the quorum decision must agree, or the log explains a
// wait with a number that did not cause it. Drives the real QuorumReached
// rather than restating its rule.
func TestQuorumCountAgreesWithQuorumReached(t *testing.T) {
	nodes := []discovery.DiscoveredNode{
		{IsControlPlane: true},  // local
		{IsControlPlane: false}, // worker peer
	}
	controlPlanePeers := 0

	for _, n := range nodes[1:] {
		if n.IsControlPlane {
			controlPlanePeers++
		}
	}

	const required = 3

	if election.QuorumReached(nodes, required) {
		t.Fatal("quorum should not be reached with one control plane")
	}

	if got := quorumCount(controlPlanePeers); got >= required {
		t.Errorf("found:%d required:%d reports quorum met while it is not",
			got, required)
	}
}
