package main

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"time"

	"github.com/google/uuid"
)

type StateStore interface {
	SetClusterSpec(ctx context.Context, spec *ClusterSpec) error
	FetchClusterState(ctx context.Context) (ClusterState, error)
	AtomicWriteClusterStatus(ctx context.Context, prevStatusUUID uuid.UUID, status ClusterStatus) error

	WriteCurrentNodeStatus(ctx context.Context, status *NodeStatus) error
}

// ClusterState holds the entire state of the cluster.
type ClusterState struct {
	Spec   ClusterSpec   `json:"spec"`
	Status ClusterStatus `json:"status"`
	Nodes  []NodeStatus  `json:"nodes"`
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

// NodeDesiredState defines the desired state for a node.
type NodeStatus struct {
	Name string `json:"name"`

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
		if err := store.AtomicWriteClusterStatus(context.Background(), oldStatus.StatusUuid, newStatus); err != nil {
			return ClusterStatus{}, fmt.Errorf("failed to write cluster status: %w", err)
		}
	}
	return newStatus, nil
}

// clusterStatusChanged checks if any meaningful fields in the cluster
// status changed (e.g. not the UUID or source node).
func clusterStatusChanged(old, new ClusterStatus) bool {
	// Clear metadata for comparison
	old.StatusUuid = uuid.UUID{}
	old.SourceNode = ""
	old.SourceNodeTime = ""

	new.StatusUuid = uuid.UUID{}
	new.SourceNode = ""
	new.SourceNodeTime = ""

	// Normalize all empty slices to nil automatically
	normalizeSlicesInStruct(reflect.ValueOf(&old).Elem())
	normalizeSlicesInStruct(reflect.ValueOf(&new).Elem())

	return !reflect.DeepEqual(old, new)
}

// normalizeSlicesInStruct converts all empty slices to nil using
// reflection. This is necessary because DeepEqual considers a nil slice
// and empty slice as not equal.
func normalizeSlicesInStruct(v reflect.Value) {
	for i := range v.NumField() {
		field := v.Field(i)
		if field.Kind() == reflect.Slice && field.Len() == 0 && !field.IsNil() {
			field.Set(reflect.Zero(field.Type())) // Set to nil
		}
	}
}

// ComputeNewClusterStatus processes the current cluster state and returns
// the updated cluster status, or nil if no changes are needed.
func ComputeNewClusterStatus(state ClusterState) ClusterStatus {
	status := state.Status

	// Handle role assignment
	status.IntendedPrimary = selectIntendedPrimary(state.Nodes, status.IntendedPrimary)
	status.IntendedReplicas = buildIntendedReplicas(state.Nodes, status.IntendedPrimary)

	// Assess health
	status.HealthReasons = computeClusterUnhealthyReasons(state.Nodes, status)
	status.Health = ClusterHealthHealthy
	if len(status.HealthReasons) > 0 {
		status.Health = ClusterHealthUnhealthy
	}

	// TODO: Handle failover state transitions

	return status
}

// selectIntendedPrimary chooses which node should be the primary
func selectIntendedPrimary(nodes []NodeStatus, currentPrimary string) string {
	// If we already have a primary and it's still in the cluster, keep it
	if currentPrimary != "" {
		for _, node := range nodes {
			if node.Name == currentPrimary {
				return currentPrimary
			}
		}
	}

	// If we have no primary or current primary left, pick the best node
	if len(nodes) > 0 {
		// Pick first node with no error
		for _, node := range nodes {
			if node.Error == nil {
				return node.Name
			}
		}

		// Fallback to first node if no healthy nodes
		return nodes[0].Name
	}

	return ""
}

// buildIntendedReplicas creates the replica list from all nodes except the primary
func buildIntendedReplicas(nodes []NodeStatus, intendedPrimary string) []string {
	var replicas []string
	for _, node := range nodes {
		if node.Name != intendedPrimary {
			replicas = append(replicas, node.Name)
		}
	}
	if len(replicas) == 0 {
		return nil
	}
	return replicas
}

// computeClusterUnhealthyReasons assesses the overall health of the cluster
func computeClusterUnhealthyReasons(nodes []NodeStatus, status ClusterStatus) []string {
	if len(nodes) == 0 {
		return []string{"No nodes in the cluster"}
	}

	var unhealthyReasons []string

	for _, node := range nodes {
		if node.Error != nil {
			reason := fmt.Sprintf("Node %s has an error", node.Name)
			unhealthyReasons = append(unhealthyReasons, reason)
		} else if node.IsPrimary {
			if node.Name != status.IntendedPrimary {
				reason := fmt.Sprintf("Node %s is marked as primary but not intended primary", node.Name)
				unhealthyReasons = append(unhealthyReasons, reason)
			}
			// Check replica statuses match
			if len(node.Replicas) != len(status.IntendedReplicas) {
				reason := fmt.Sprintf(
					"Node %s has %d replica statuses but there are %d intended replicas",
					node.Name,
					len(node.Replicas),
					len(status.IntendedReplicas),
				)
				unhealthyReasons = append(unhealthyReasons, reason)
			}
		} else {
			// Node is replica
			if !slices.Contains(status.IntendedReplicas, node.Name) {
				reason := fmt.Sprintf("Node %s is not in the intended replicas list", node.Name)
				unhealthyReasons = append(unhealthyReasons, reason)
			}
			// Should be replicating to primary
			if node.ReplicationStatus == nil {
				reason := fmt.Sprintf("Node %s has no replication status", node.Name)
				unhealthyReasons = append(unhealthyReasons, reason)
			}
		}
	}

	return unhealthyReasons
}
