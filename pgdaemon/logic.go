package main

import (
	"context"
	"pgdaemon/election"
)

type StateStore interface {
	election.ElectionBackend

	SetClusterSpec(ctx context.Context, spec *ClusterSpec) error
	FetchClusterSpec(ctx context.Context) (*ClusterSpec, error)

	WriteCurrentNodeStatus(ctx context.Context, status *NodeStatus) error
}

// ClusterSpec defines the desired state of the cluster.
type ClusterSpec struct {
	PrimaryName  string   `json:"primary_name"`
	ReplicaNames []string `json:"replica_names"`
}

// NodeDesiredState defines the desired state for a node.
type NodeStatus struct {
	Error             *string                `json:"error,omitempty"`
	NodeTime          string                 `json:"node_time"`
	IsPrimary         bool                   `json:"is_primary"`
	Replicas          []NodeReplicas         `json:"replicas,omitempty"`
	ReplicationStatus *NodeReplicationStatus `json:"replication_status,omitempty"`
}

type NodeReplicas struct {
	Hostname  string  `json:"hostname"`
	State     string  `json:"state"`
	WriteLsn  string  `json:"write_lsn"`
	WriteLag  *string `json:"write_lag"`
	SyncState string  `json:"sync_state"`
	ReplyTime string  `json:"reply_time"`
}

type NodeReplicationStatus struct {
	PrimaryHost string  `json:"primary_host"`
	Status      string  `json:"status"`
	WrittenLsn  *string `json:"written_lsn"`
}
