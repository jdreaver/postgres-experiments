package main

import (
	"context"

	"pgdaemon/election"
)

type StateStore interface {
	election.ElectionBackend

	InitializeCluster(ctx context.Context, spec *ClusterSpec) error
	FetchClusterSpec(ctx context.Context) (*ClusterSpec, error)

	WriteCurrentNodeStatus(ctx context.Context, status *NodeStatus) error
}
