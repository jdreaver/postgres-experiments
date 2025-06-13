package main

import "context"

type StateStore interface {
	ElectionBackend

	InitializeCluster(ctx context.Context, spec *ClusterSpec) error
	FetchClusterSpec(ctx context.Context) (*ClusterSpec, error)

	WriteCurrentNodeStatus(ctx context.Context, status *NodeStatus) error
}
