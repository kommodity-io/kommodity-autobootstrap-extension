package main

import (
	"testing"
	"time"
)

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
