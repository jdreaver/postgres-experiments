package main

import "context"

type StateStore interface {
	RunElection(ctx context.Context) error
	IsLeader() bool

	InitializeCluster(ctx context.Context, spec *ClusterSpec) error
	FetchClusterSpec(ctx context.Context) (*ClusterSpec, error)

	WriteCurrentNodeStatus(ctx context.Context, status *NodeStatus) error

	SetNodeSpec(ctx context.Context, nodeName string, spec *NodeSpec) error
	FetchCurrentNodeSpec(ctx context.Context) (*NodeSpec, error)
}
