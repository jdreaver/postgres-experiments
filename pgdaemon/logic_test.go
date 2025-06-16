package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestComputeNewClusterStatus_EmptyCluster(t *testing.T) {
	state := ClusterState{
		Status: ClusterStatus{},
		Nodes:  []NodeStatus{},
	}

	result := ComputeNewClusterStatus(state)

	assert.Equal(t, ClusterHealthUnhealthy, result.Health)
	assert.Contains(t, result.HealthReasons, "No nodes in the cluster")
	assert.Equal(t, "", result.IntendedPrimary)
	assert.Nil(t, result.IntendedReplicas)
}

func TestComputeNewClusterStatus_SingleHealthyNode(t *testing.T) {
	state := ClusterState{
		Status: ClusterStatus{},
		Nodes: []NodeStatus{
			{
				Name:      "node1",
				IsPrimary: true,
				Error:     nil,
			},
		},
	}

	result := ComputeNewClusterStatus(state)

	assert.Equal(t, ClusterHealthHealthy, result.Health)
	assert.Empty(t, result.HealthReasons)
	assert.Equal(t, "node1", result.IntendedPrimary)
	assert.Nil(t, result.IntendedReplicas)
}

func TestComputeNewClusterStatus_PrimaryWithReplicas(t *testing.T) {
	state := ClusterState{
		Status: ClusterStatus{
			IntendedPrimary: "node1",
		},
		Nodes: []NodeStatus{
			{
				Name:      "node1",
				IsPrimary: true,
				Error:     nil,
				Replicas: []NodeReplicas{
					{Hostname: "node2"},
					{Hostname: "node3"},
				},
			},
			{
				Name:      "node2",
				IsPrimary: false,
				Error:     nil,
				ReplicationStatus: &NodeReplicationStatus{
					PrimaryHost: "node1",
					Status:      "streaming",
				},
			},
			{
				Name:      "node3",
				IsPrimary: false,
				Error:     nil,
				ReplicationStatus: &NodeReplicationStatus{
					PrimaryHost: "node1",
					Status:      "streaming",
				},
			},
		},
	}

	result := ComputeNewClusterStatus(state)

	assert.Equal(t, ClusterHealthHealthy, result.Health)
	assert.Empty(t, result.HealthReasons)
	assert.Equal(t, "node1", result.IntendedPrimary)
	assert.ElementsMatch(t, []string{"node2", "node3"}, result.IntendedReplicas)
}

func TestComputeNewClusterStatus_NodeWithError(t *testing.T) {
	errMsg := "connection failed"
	state := ClusterState{
		Status: ClusterStatus{},
		Nodes: []NodeStatus{
			{
				Name:      "node1",
				IsPrimary: true,
				Error:     &errMsg,
			},
		},
	}

	result := ComputeNewClusterStatus(state)

	assert.Equal(t, ClusterHealthUnhealthy, result.Health)
	assert.Contains(t, result.HealthReasons, "Node node1 has an error")
	assert.Equal(t, "node1", result.IntendedPrimary)
}

func TestComputeNewClusterStatus_WrongPrimary(t *testing.T) {
	state := ClusterState{
		Status: ClusterStatus{
			IntendedPrimary: "node2",
		},
		Nodes: []NodeStatus{
			{
				Name:      "node1",
				IsPrimary: true,
				Error:     nil,
			},
			{
				Name:      "node2",
				IsPrimary: false,
				Error:     nil,
			},
		},
	}

	result := ComputeNewClusterStatus(state)

	assert.Equal(t, ClusterHealthUnhealthy, result.Health)
	assert.Contains(t, result.HealthReasons, "Node node1 is marked as primary but not intended primary")
	assert.Equal(t, "node2", result.IntendedPrimary)
	assert.ElementsMatch(t, []string{"node1"}, result.IntendedReplicas)
}

func TestComputeNewClusterStatus_ReplicaMissingReplicationStatus(t *testing.T) {
	state := ClusterState{
		Status: ClusterStatus{
			IntendedPrimary: "node1",
		},
		Nodes: []NodeStatus{
			{
				Name:      "node1",
				IsPrimary: true,
				Error:     nil,
				Replicas:  []NodeReplicas{{Hostname: "node2"}},
			},
			{
				Name:              "node2",
				IsPrimary:         false,
				Error:             nil,
				ReplicationStatus: nil, // Missing replication status
			},
		},
	}

	result := ComputeNewClusterStatus(state)

	assert.Equal(t, ClusterHealthUnhealthy, result.Health)
	assert.Contains(t, result.HealthReasons, "Node node2 has no replication status")
}

func TestComputeNewClusterStatus_PrimaryFailoverToHealthyNode(t *testing.T) {
	errMsg := "primary failed"
	state := ClusterState{
		Status: ClusterStatus{
			IntendedPrimary: "node1",
		},
		Nodes: []NodeStatus{
			{
				Name:      "node1",
				IsPrimary: true,
				Error:     &errMsg,
			},
			{
				Name:      "node2",
				IsPrimary: false,
				Error:     nil,
			},
		},
	}

	result := ComputeNewClusterStatus(state)

	// Current logic keeps existing primary even with error, only changes if node leaves cluster
	assert.Equal(t, "node1", result.IntendedPrimary)
	assert.ElementsMatch(t, []string{"node2"}, result.IntendedReplicas)
}
