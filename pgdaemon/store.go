package main

import "context"

type StateStore interface {
	RunElection(ctx context.Context) error
	IsLeader() bool

	InitializeCluster(ctx context.Context, state *ClusterDesiredState) error
	FetchClusterDesiredState(ctx context.Context) (*ClusterDesiredState, error)

	WriteCurrentNodeObservedState(ctx context.Context, state *NodeObservedState) error

	SetNodeDesiredState(ctx context.Context, nodeName string, state *NodeDesiredState) error
	FetchCurrentNodeDesiredState(ctx context.Context) (*NodeDesiredState, error)
}
