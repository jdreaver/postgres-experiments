package main

import (
	"context"
	"fmt"
	"pgdaemon/election"
	"slices"
	"time"

	"github.com/google/uuid"
)

type StateStore interface {
	election.ElectionBackend

	SetClusterSpec(ctx context.Context, spec *ClusterSpec) error
	FetchClusterState(ctx context.Context) (ClusterState, error)
	WriteClusterStatus(ctx context.Context, prevStatusUUID uuid.UUID, status ClusterStatus) error

	WriteCurrentNodeStatus(ctx context.Context, status *NodeStatus) error
}

// ClusterState holds the entire state of the cluster.
type ClusterState struct {
	Spec   ClusterSpec            `json:"spec"`
	Status ClusterStatus          `json:"status"`
	Nodes  map[string]*NodeStatus `json:"nodes"`
}

// ClusterSpec defines the desired state of the cluster.
type ClusterSpec struct{}

type ClusterHealth string

const (
	ClusterHealthHealthy   ClusterHealth = "healthy"
	ClusterHealthUnhealthy ClusterHealth = "unhealthy"
)

// ClusterStatus defines the current status of the cluster.
type ClusterStatus struct {
	// StatusUuid is a unique identifier used in compare-and-swap
	// operations on this status so nodes can detect if another node
	// has written a newer status. This allows us to make decisions
	// about state without a specific leader and dealing with leader
	// election.
	StatusUuid uuid.UUID `json:"status_uuid"`

	// SourceNode is the name of the node that last updated this
	// cluster status.
	SourceNode string `json:"source_node"`

	// SourceNodeTime is the time reported by the source node when
	// it last updated this cluster status. This is purely for
	// informational purposes to aid humans in debugging.
	SourceNodeTime string `json:"source_node_time,omitempty"`

	Health        ClusterHealth `json:"health"`
	HealthReasons []string      `json:"health_reasons,omitempty"`

	// IntendedPrimary is the node that the cluster has decided
	// should be the primary, although this may differ from the
	// current primary during failovers.
	IntendedPrimary  string   `json:"intended_primary"`
	IntendedReplicas []string `json:"intended_replicas"`
}

// clusterStatusChanged checks if any meaningful fields in the cluster
// status changed (e.g. not the UUID or source node).
func clusterStatusChanged(old, new ClusterStatus) bool {
	if old.Health != new.Health {
		return true
	}

	if len(old.HealthReasons) != len(new.HealthReasons) {
		return true
	}
	for i, reason := range old.HealthReasons {
		if reason != new.HealthReasons[i] {
			return true
		}
	}

	if old.IntendedPrimary != new.IntendedPrimary {
		return true
	}

	if len(old.IntendedReplicas) != len(new.IntendedReplicas) {
		return true
	}
	for i, replica := range old.IntendedReplicas {
		if replica != new.IntendedReplicas[i] {
			return true
		}
	}

	return false
}

// NodeDesiredState defines the desired state for a node.
type NodeStatus struct {
	// StatusUuid is a unique identifier for this status so nodes
	// can detect if another node has written a newer status.
	StatusUuid uuid.UUID `json:"status_uuid"`

	// NodeTime is the current time, as reported by the node. This
	// is purely for informational purposes to aid humans in
	// debugging. Only local, monotonic clocks are used for business
	// logic.
	NodeTime string `json:"node_time"`

	Error             *string                `json:"error,omitempty"`
	IsPrimary         bool                   `json:"is_primary"`
	Replicas          []NodeReplicas         `json:"replicas,omitempty"`
	ReplicationStatus *NodeReplicationStatus `json:"replication_status,omitempty"`
}

type NodeReplicas struct {
	Hostname  string  `json:"hostname"`
	State     string  `json:"state"`
	WriteLsn  *string `json:"write_lsn"`
	WriteLag  *string `json:"write_lag"`
	SyncState *string `json:"sync_state"`
	ReplyTime *string `json:"reply_time"`
}

type NodeReplicationStatus struct {
	PrimaryHost string  `json:"primary_host"`
	Status      string  `json:"status"`
	WrittenLsn  *string `json:"written_lsn"`
}

func WriteClusterStatusIfChanged(store StateStore, oldStatus ClusterStatus, newStatus ClusterStatus, nodeName string) (ClusterStatus, error) {
	if clusterStatusChanged(oldStatus, newStatus) {
		newStatus.StatusUuid = uuid.New()
		newStatus.SourceNode = nodeName
		newStatus.SourceNodeTime = time.Now().Format(time.RFC3339)
		if err := store.WriteClusterStatus(context.Background(), oldStatus.StatusUuid, newStatus); err != nil {
			return ClusterStatus{}, fmt.Errorf("failed to write cluster status: %w", err)
		}
	}
	return newStatus, nil
}

// ComputeNewClusterStatus processes the current cluster state and returns
// the updated cluster status, or nil if no changes are needed.
func ComputeNewClusterStatus(state ClusterState) ClusterStatus {
	status := state.Status

	// Sort node names for consistent ordering
	var nodeNames []string
	for nodeName := range state.Nodes {
		nodeNames = append(nodeNames, nodeName)
	}
	slices.Sort(nodeNames)

	// If we have no primary, pick the best node as the primary
	if status.IntendedPrimary == "" && len(state.Nodes) > 0 {
		// Pick first node with no error. TODO: Should probably
		// pick the node with the most recent healthy status.
		for _, nodeName := range nodeNames {
			if node := state.Nodes[nodeName]; node.Error == nil {
				status.IntendedPrimary = nodeName
			}
		}

		// Fallback to first node if no healthy nodes
		if status.IntendedPrimary == "" {
			status.IntendedPrimary = nodeNames[0]
		}
	}

	// Rebuild replica list from scratch to handle nodes that left the cluster
	var newReplicas []string
	for _, nodeName := range nodeNames {
		if nodeName != status.IntendedPrimary {
			newReplicas = append(newReplicas, nodeName)
		}
	}
	status.IntendedReplicas = newReplicas

	// If any node is unhealthy, set the cluster state to unhealthy.
	// Otherwise, set it to healthy.
	allNodesHealthy := true
	unhealthyReasons := []string{}
	if len(nodeNames) == 0 {
		allNodesHealthy = false
		unhealthyReasons = append(unhealthyReasons, "No nodes in the cluster")
	}
	for _, nodeName := range nodeNames {
		nodeStatus := state.Nodes[nodeName]

		if nodeStatus.Error != nil {
			allNodesHealthy = false
			reason := fmt.Sprintf("Node %s has an error", nodeName)
			unhealthyReasons = append(unhealthyReasons, reason)
		} else if nodeStatus.IsPrimary {
			if nodeName != status.IntendedPrimary {
				allNodesHealthy = false
				reason := fmt.Sprintf("Node %s is marked as primary but not intended primary", nodeName)
				unhealthyReasons = append(unhealthyReasons, reason)
			}
			// Check replica statuses match.
			// TODO: Check that the actual names match too!
			if len(nodeStatus.Replicas) != len(status.IntendedReplicas) {
				allNodesHealthy = false
				reason := fmt.Sprintf(
					"Node %s has %d replica statuses but there are %d intended replicas",
					nodeName,
					len(nodeStatus.Replicas),
					len(status.IntendedReplicas),
				)
				unhealthyReasons = append(unhealthyReasons, reason)
			}
		} else {
			// Node is replica
			if !slices.Contains(status.IntendedReplicas, nodeName) {
				allNodesHealthy = false
				reason := fmt.Sprintf("Node %s is not in the intended replicas list", nodeName)
				unhealthyReasons = append(unhealthyReasons, reason)
			}
			// Should be replicating to primary
			if nodeStatus.ReplicationStatus == nil {
				allNodesHealthy = false
				reason := fmt.Sprintf("Node %s has no replication status", nodeName)
				unhealthyReasons = append(unhealthyReasons, reason)
			}
		}
	}

	if !allNodesHealthy {
		status.Health = ClusterHealthUnhealthy
		status.HealthReasons = unhealthyReasons
	} else {
		status.Health = ClusterHealthHealthy
		status.HealthReasons = []string{}
	}

	return status
}
