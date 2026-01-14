package election

import (
	"net/netip"
	"testing"
	"time"

	"github.com/kommodity/talos-auto-bootstrap/pkg/discovery"
)

func TestElectLeader_OldestNodeWins(t *testing.T) {
	now := time.Now()

	localNode := discovery.DiscoveredNode{
		IP:             netip.MustParseAddr("192.168.1.10"),
		IsControlPlane: true,
		CreationTime:   now.Add(5 * time.Second), // 5 seconds newer
		Hostname:       "node-a",
	}

	peers := []discovery.DiscoveredNode{
		{
			IP:             netip.MustParseAddr("192.168.1.11"),
			IsControlPlane: true,
			CreationTime:   now, // oldest
			Hostname:       "node-b",
		},
		{
			IP:             netip.MustParseAddr("192.168.1.12"),
			IsControlPlane: true,
			CreationTime:   now.Add(3 * time.Second),
			Hostname:       "node-c",
		},
	}

	result := ElectLeader(localNode, peers)

	if result.IsLeader {
		t.Error("expected local node not to be leader")
	}
	if result.Leader.IP.String() != "192.168.1.11" {
		t.Errorf("expected leader to be 192.168.1.11, got %s", result.Leader.IP)
	}
	if len(result.Candidates) != 3 {
		t.Errorf("expected 3 candidates, got %d", len(result.Candidates))
	}
}

func TestElectLeader_TieBreakByIP(t *testing.T) {
	now := time.Now()

	localNode := discovery.DiscoveredNode{
		IP:             netip.MustParseAddr("192.168.1.12"),
		IsControlPlane: true,
		CreationTime:   now, // same time
		Hostname:       "node-a",
	}

	peers := []discovery.DiscoveredNode{
		{
			IP:             netip.MustParseAddr("192.168.1.10"), // lowest IP
			IsControlPlane: true,
			CreationTime:   now, // same time
			Hostname:       "node-b",
		},
		{
			IP:             netip.MustParseAddr("192.168.1.11"),
			IsControlPlane: true,
			CreationTime:   now, // same time
			Hostname:       "node-c",
		},
	}

	result := ElectLeader(localNode, peers)

	if result.IsLeader {
		t.Error("expected local node not to be leader")
	}
	if result.Leader.IP.String() != "192.168.1.10" {
		t.Errorf("expected leader to be 192.168.1.10 (lowest IP), got %s", result.Leader.IP)
	}
}

func TestElectLeader_SingleNode(t *testing.T) {
	localNode := discovery.DiscoveredNode{
		IP:             netip.MustParseAddr("192.168.1.10"),
		IsControlPlane: true,
		CreationTime:   time.Now(),
		Hostname:       "node-a",
	}

	result := ElectLeader(localNode, nil)

	if !result.IsLeader {
		t.Error("expected single node to be leader")
	}
	if result.Leader.IP.String() != "192.168.1.10" {
		t.Errorf("expected leader to be 192.168.1.10, got %s", result.Leader.IP)
	}
	if len(result.Candidates) != 1 {
		t.Errorf("expected 1 candidate, got %d", len(result.Candidates))
	}
}

func TestElectLeader_LocalNodeIsOldest(t *testing.T) {
	now := time.Now()

	localNode := discovery.DiscoveredNode{
		IP:             netip.MustParseAddr("192.168.1.10"),
		IsControlPlane: true,
		CreationTime:   now, // oldest
		Hostname:       "node-a",
	}

	peers := []discovery.DiscoveredNode{
		{
			IP:             netip.MustParseAddr("192.168.1.11"),
			IsControlPlane: true,
			CreationTime:   now.Add(5 * time.Second),
			Hostname:       "node-b",
		},
		{
			IP:             netip.MustParseAddr("192.168.1.12"),
			IsControlPlane: true,
			CreationTime:   now.Add(10 * time.Second),
			Hostname:       "node-c",
		},
	}

	result := ElectLeader(localNode, peers)

	if !result.IsLeader {
		t.Error("expected local node to be leader (oldest)")
	}
	if result.Leader.IP.String() != "192.168.1.10" {
		t.Errorf("expected leader to be 192.168.1.10, got %s", result.Leader.IP)
	}
}

func TestElectLeader_OnlyControlPlaneParticipate(t *testing.T) {
	now := time.Now()

	localNode := discovery.DiscoveredNode{
		IP:             netip.MustParseAddr("192.168.1.10"),
		IsControlPlane: true,
		CreationTime:   now.Add(10 * time.Second), // newest CP
		Hostname:       "cp-a",
	}

	peers := []discovery.DiscoveredNode{
		{
			IP:             netip.MustParseAddr("192.168.1.11"),
			IsControlPlane: false, // worker - should be excluded
			CreationTime:   now,   // oldest but worker
			Hostname:       "worker-a",
		},
		{
			IP:             netip.MustParseAddr("192.168.1.12"),
			IsControlPlane: true,
			CreationTime:   now.Add(5 * time.Second),
			Hostname:       "cp-b",
		},
	}

	result := ElectLeader(localNode, peers)

	// Only 2 control plane nodes should be candidates
	if len(result.Candidates) != 2 {
		t.Errorf("expected 2 candidates (only control plane), got %d", len(result.Candidates))
	}

	// cp-b should be leader (older than cp-a)
	if result.Leader.IP.String() != "192.168.1.12" {
		t.Errorf("expected leader to be 192.168.1.12, got %s", result.Leader.IP)
	}
	if result.IsLeader {
		t.Error("expected local node not to be leader")
	}
}

func TestQuorumReached(t *testing.T) {
	tests := []struct {
		name       string
		candidates []discovery.DiscoveredNode
		minNodes   int
		expected   bool
	}{
		{
			name: "quorum met exactly",
			candidates: []discovery.DiscoveredNode{
				{IsControlPlane: true},
				{IsControlPlane: true},
				{IsControlPlane: true},
			},
			minNodes: 3,
			expected: true,
		},
		{
			name: "quorum exceeded",
			candidates: []discovery.DiscoveredNode{
				{IsControlPlane: true},
				{IsControlPlane: true},
				{IsControlPlane: true},
			},
			minNodes: 2,
			expected: true,
		},
		{
			name: "quorum not met",
			candidates: []discovery.DiscoveredNode{
				{IsControlPlane: true},
				{IsControlPlane: true},
			},
			minNodes: 3,
			expected: false,
		},
		{
			name: "workers don't count",
			candidates: []discovery.DiscoveredNode{
				{IsControlPlane: true},
				{IsControlPlane: false},
				{IsControlPlane: false},
			},
			minNodes: 2,
			expected: false,
		},
		{
			name:       "empty candidates",
			candidates: []discovery.DiscoveredNode{},
			minNodes:   1,
			expected:   false,
		},
		{
			name: "single node sufficient",
			candidates: []discovery.DiscoveredNode{
				{IsControlPlane: true},
			},
			minNodes: 1,
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := QuorumReached(tt.candidates, tt.minNodes)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}
