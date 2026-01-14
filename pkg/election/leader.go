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
// 2. Sort by boot time (ascending - oldest first)
// 3. Tie-break by IP address (lowest wins)
// 4. First node in sorted list is the leader
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

	// Sort by creation time, then by IP for deterministic tie-breaking
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].CreationTime.Equal(candidates[j].CreationTime) {
			// Tie-break by IP address (lowest wins)
			return candidates[i].IP.Less(candidates[j].IP)
		}
		// Oldest node (earliest creation time) wins
		return candidates[i].CreationTime.Before(candidates[j].CreationTime)
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
