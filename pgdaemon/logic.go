package main

import (
	"context"
	"pgdaemon/election"

	"github.com/google/uuid"
)

type StateStore interface {
	election.ElectionBackend

	SetClusterSpec(ctx context.Context, spec *ClusterSpec) error
	FetchClusterState(ctx context.Context) (*ClusterState, error)

	WriteCurrentNodeStatus(ctx context.Context, status *NodeStatus) error
}

// ClusterState holds the entire state of the cluster.
type ClusterState struct {
	Spec  *ClusterSpec           `json:"spec"`
	Nodes map[string]*NodeStatus `json:"nodes"`
}

// ClusterSpec defines the desired state of the cluster.
type ClusterSpec struct {
	PrimaryName  string   `json:"primary_name"`
	ReplicaNames []string `json:"replica_names"`
}

// NodeDesiredState defines the desired state for a node.
type NodeStatus struct {
	// StatusUuid is a unique identifier for this status so nodes
	// can detect if another node has written a newer status.
	StatusUuid        uuid.UUID              `json:"status_uuid"`
	Error             *string                `json:"error,omitempty"`
	NodeTime          string                 `json:"node_time"`
	IsPrimary         bool                   `json:"is_primary"`
	Replicas          []NodeReplicas         `json:"replicas,omitempty"`
	ReplicationStatus *NodeReplicationStatus `json:"replication_status,omitempty"`
}

type NodeReplicas struct {
	Hostname  string  `json:"hostname"`
	State     string  `json:"state"`
	WriteLsn  *string `json:"write_lsn"`
	WriteLag  *string `json:"write_lag"`
	SyncState string  `json:"sync_state"`
	ReplyTime string  `json:"reply_time"`
}

type NodeReplicationStatus struct {
	PrimaryHost string  `json:"primary_host"`
	Status      string  `json:"status"`
	WrittenLsn  *string `json:"written_lsn"`
}
