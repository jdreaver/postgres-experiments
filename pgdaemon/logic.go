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

type ClusterStatusState string

const (
	ClusterStatusStateHealthy   ClusterStatusState = "healthy"
	ClusterStatusStateUnhealthy ClusterStatusState = "unhealthy"
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

	State        ClusterStatusState `json:"state"`
	StateReasons []string           `json:"state_reasons,omitempty"`

	// IntendedPrimary is the node that the cluster has decided
	// should be the primary, although this may differ from the
	// current primary during failovers.
	IntendedPrimary  string   `json:"intended_primary"`
	IntendedReplicas []string `json:"intended_replicas"`
}

// clusterStatusChanged checks if any meaningful fields in the cluster
// status changed (e.g. not the UUID or source node).
func clusterStatusChanged(old, new ClusterStatus) bool {
	if old.State != new.State {
		return true
	}

	if len(old.StateReasons) != len(new.StateReasons) {
		return true
	}
	for i, reason := range old.StateReasons {
		if reason != new.StateReasons[i] {
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

// ClusterStateMachine processes the current cluster state and returns the updated cluster status, or nil if no changes are needed.
func ClusterStateMachine(state ClusterState, nodeName string) (*ClusterStatus, error) {
	return clusterStateMachineInner(state, nodeName, uuid.New, time.Now)
}

func clusterStateMachineInner(state ClusterState, nodeName string, makeUuid func() uuid.UUID, makeNow func() time.Time) (*ClusterStatus, error) {
	status := state.Status

	// Ensure we have some status to start with
	if status.State == "" {
		status = ClusterStatus{
			State:        ClusterStatusStateUnhealthy,
			StateReasons: []string{"Cluster state is nil or missing status"},
		}
	} else {
		status = state.Status
	}

	// Sort node names for consistent ordering
	var nodeNames []string
	for nodeName := range state.Nodes {
		nodeNames = append(nodeNames, nodeName)
	}
	slices.Sort(nodeNames)

	// If we have no primary, pick the first node as the primary
	// arbitrarily. (TODO: We pick the first node because we need to
	// bootstrap empty clusters, but we should probably do
	// something more sophisticated in the future.)
	if status.IntendedPrimary == "" && len(state.Nodes) > 0 {
		for _, nodeName := range nodeNames {
			status.IntendedPrimary = nodeName
			break
		}
	}

	// For all other nodes, make them replicas
	for _, nodeName := range nodeNames {
		if nodeName == status.IntendedPrimary {
			continue // Skip the primary
		}
		if !slices.Contains(status.IntendedReplicas, nodeName) {
			status.IntendedReplicas = append(status.IntendedReplicas, nodeName)
		}
	}

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
			// Check if there should be replicas
			if len(nodeStatus.Replicas) == 0 && len(status.IntendedReplicas) > 0 {
				allNodesHealthy = false
				reason := fmt.Sprintf("Node %s has no replication status but there are %d intended replicas", nodeName, len(status.IntendedReplicas))
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
		status.State = ClusterStatusStateUnhealthy
		status.StateReasons = unhealthyReasons
	} else {
		status.State = ClusterStatusStateHealthy
		status.StateReasons = []string{}
	}

	if clusterStatusChanged(state.Status, status) {
		status.StatusUuid = makeUuid()
		status.SourceNode = nodeName
		status.SourceNodeTime = makeNow().Format(time.RFC3339)
		return &status, nil
	}
	return nil, nil
}
