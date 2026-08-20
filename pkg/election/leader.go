package election

import (
	"sort"

	"github.com/kommodity/talos-auto-bootstrap/pkg/discovery"
)

// ElectionResult contains the outcome of a leader election.
type ElectionResult struct {
	// Leader is the elected leader node
	Leader *discovery.DiscoveredNode
	// IsLeader is true if the local node is the elected leader
	IsLeader bool
	// Candidates is the list of all participating control plane nodes
	Candidates []discovery.DiscoveredNode
}

// ElectLeader performs deterministic leader election among control plane nodes.
// The election algorithm:
// 1. Collect all control plane nodes (local + peers)
// 2. Sort by IP address (lowest wins)
//
// CreationTime is intentionally ignored because the local node and peers use
// different time sources (local: /proc/stat boot time, peers: Talos Version.Built
// or time.Now() if COSI is unavailable), making cross-node comparison unreliable
// and causing every node to elect itself as leader.
func ElectLeader(localNode discovery.DiscoveredNode,
	peers []discovery.DiscoveredNode) *ElectionResult {

	// Collect all control plane candidates
	candidates := make([]discovery.DiscoveredNode, 0, len(peers)+1)
	candidates = append(candidates, localNode)

	for _, peer := range peers {
		if peer.IsControlPlane {
			candidates = append(candidates, peer)
		}
	}

	// Sort by IP address (lowest wins) for deterministic, consistent election
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].IP.Less(candidates[j].IP)
	})

	leader := &candidates[0]

	return &ElectionResult{
		Leader:     leader,
		IsLeader:   leader.IP == localNode.IP,
		Candidates: candidates,
	}
}

// QuorumReached checks if the minimum number of control plane nodes is available.
func QuorumReached(candidates []discovery.DiscoveredNode, minNodes int) bool {
	count := 0
	for _, c := range candidates {
		if c.IsControlPlane {
			count++
		}
	}
	return count >= minNodes
}
