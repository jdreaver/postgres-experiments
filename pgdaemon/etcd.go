package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/google/uuid"
	clientv3 "go.etcd.io/etcd/client/v3"
)

type EtcdBackend struct {
	client      *clientv3.Client
	clusterName string
	nodeName    string
}

func NewEtcdBackend(client *clientv3.Client, clusterName string, nodeName string) *EtcdBackend {
	return &EtcdBackend{
		clusterName: clusterName,
		client:      client,
		nodeName:    nodeName,
	}
}

func (etcd *EtcdBackend) clusterPrefix() string {
	return "/" + etcd.clusterName
}

func (etcd *EtcdBackend) clusterSpecPrefix() string {
	return etcd.clusterPrefix() + "/spec"
}

func (etcd *EtcdBackend) clusterStatusUuidPrefix() string {
	return etcd.clusterPrefix() + "/status-uuid"
}

func (etcd *EtcdBackend) clusterStatusPrefix() string {
	return etcd.clusterPrefix() + "/status"
}

func (etcd *EtcdBackend) nodeStatusesPrefix() string {
	return etcd.clusterPrefix() + "/node-statuses"
}

func (etcd *EtcdBackend) nodeStatusPrefix(nodeName string) string {
	return etcd.nodeStatusesPrefix() + "/" + nodeName
}

func (etcd *EtcdBackend) AtomicWriteClusterStatus(ctx context.Context, prevStatusUUID uuid.UUID, status ClusterStatus) error {
	compare := clientv3.Compare(clientv3.CreateRevision(etcd.clusterStatusUuidPrefix()), "=", 0)
	if prevStatusUUID != uuid.Nil {
		compare = clientv3.Compare(clientv3.Value(etcd.clusterStatusUuidPrefix()), "=", prevStatusUUID.String())
	}

	statusBytes, err := json.Marshal(status)
	if err != nil {
		return fmt.Errorf("failed to marshal cluster status: %w", err)
	}

	txn := etcd.client.Txn(ctx)
	_, err = txn.If(
		compare,
	).Then(
		clientv3.OpPut(etcd.clusterStatusUuidPrefix(), status.StatusUuid.String()),
		clientv3.OpPut(etcd.clusterStatusPrefix(), string(statusBytes)),
	).Commit()
	if err != nil {
		return fmt.Errorf("failed to commit cluster status transaction: %w", err)
	}

	return nil
}

func (etcd *EtcdBackend) WriteCurrentNodeStatus(ctx context.Context, status *NodeStatus) error {
	statusBytes, err := json.Marshal(status)
	if err != nil {
		return fmt.Errorf("failed to marshal node status: %w", err)
	}

	if _, err := etcd.client.Put(ctx, etcd.nodeStatusPrefix(etcd.nodeName), string(statusBytes)); err != nil {
		return fmt.Errorf("failed to write node status to etcd: %w", err)
	}

	return nil
}

func (etcd *EtcdBackend) SetClusterSpec(ctx context.Context, spec *ClusterSpec) error {
	specBytes, err := json.Marshal(spec)
	if err != nil {
		return fmt.Errorf("failed to marshal cluster spec: %w", err)
	}

	if _, err := etcd.client.Put(ctx, etcd.clusterSpecPrefix(), string(specBytes)); err != nil {
		return fmt.Errorf("failed to write cluster spec to etcd: %w", err)
	}

	log.Printf("Cluster spec set: %s", string(specBytes))

	return nil
}

func (etcd *EtcdBackend) FetchClusterState(ctx context.Context) (ClusterState, error) {
	var state ClusterState

	resp, err := etcd.client.Get(ctx, etcd.clusterPrefix(), clientv3.WithPrefix())
	if err != nil {
		return state, fmt.Errorf("failed to get cluster state from etcd: %w", err)
	}

	if len(resp.Kvs) == 0 {
		return state, fmt.Errorf("cluster state not found")
	}

	state.Nodes = []NodeStatus{}
	for _, kv := range resp.Kvs {
		if string(kv.Key) == etcd.clusterSpecPrefix() {
			if err := json.Unmarshal(kv.Value, &state.Spec); err != nil {
				return state, fmt.Errorf("failed to unmarshal cluster spec: %w", err)
			}
		} else if string(kv.Key) == etcd.clusterStatusPrefix() {
			if err := json.Unmarshal(kv.Value, &state.Status); err != nil {
				return state, fmt.Errorf("failed to unmarshal cluster status: %w", err)
			}
		} else if string(kv.Key) == etcd.clusterStatusUuidPrefix() {
			// Ignore status UUID key in cluster state. UUID is embedded in status.
		} else if strings.HasPrefix(string(kv.Key), etcd.nodeStatusesPrefix()) {
			nodeName := strings.TrimPrefix(string(kv.Key), etcd.nodeStatusesPrefix()+"/")
			var nodeStatus NodeStatus
			if err := json.Unmarshal(kv.Value, &nodeStatus); err != nil {
				return state, fmt.Errorf("failed to unmarshal node status for %s: %w", nodeName, err)
			}

			if nodeName != nodeStatus.Name {
				return state, fmt.Errorf("node status name mismatch: expected %s, got %s", nodeName, nodeStatus.Name)
			}

			state.Nodes = append(state.Nodes, nodeStatus)
		} else {
			log.Printf("WARNING: Ignoring unexpected key in cluster prefix: %s", kv.Key)
		}
	}

	return state, nil
}

//
// Old etcd leader election code
//

// const etcdRvnKey = "/rvn"
// const etcdLeaderKey = "/leader"
// const etcdDurationMsKey = "/lease_duration_ms"

// func (etcd *EtcdBackend) electionPrefix() string {
// 	return etcd.clusterPrefix() + "/election"
// }

// func (etcd *EtcdBackend) AtomicCompareAndSwapLease(ctx context.Context, prevRVN *uuid.UUID, newLease election.Lease) (bool, error) {
// 	compare := clientv3.Compare(clientv3.CreateRevision(etcd.electionPrefix()+etcdRvnKey), "=", 0)
// 	if prevRVN != nil {
// 		lastRVN := *prevRVN
// 		compare = clientv3.Compare(clientv3.Value(etcd.electionPrefix()+etcdRvnKey), "=", lastRVN.String())
// 	}

// 	txn := etcd.client.Txn(ctx)
// 	txnResp, err := txn.If(
// 		compare,
// 	).Then(
// 		clientv3.OpPut(etcd.electionPrefix()+etcdRvnKey, newLease.RevisionVersionNumber.String()),
// 		clientv3.OpPut(etcd.electionPrefix()+etcdLeaderKey, newLease.Leader),
// 		clientv3.OpPut(etcd.electionPrefix()+etcdDurationMsKey, fmt.Sprintf("%d", newLease.Duration.Milliseconds())),
// 	).Commit()
// 	if err != nil {
// 		return false, fmt.Errorf("failed to commit transaction: %w", err)
// 	}

// 	return txnResp.Succeeded, nil
// }

// func (etcd *EtcdBackend) FetchCurrentLease(ctx context.Context) (*election.Lease, error) {
// 	getResp, err := etcd.client.Get(ctx, etcd.electionPrefix(), clientv3.WithPrefix())
// 	if err != nil {
// 		return nil, fmt.Errorf("failed to get election key from etcd: %w", err)
// 	}

// 	if len(getResp.Kvs) == 0 {
// 		return nil, nil
// 	}

// 	var lease election.Lease
// 	for _, kv := range getResp.Kvs {
// 		if string(kv.Key) == etcd.electionPrefix()+etcdRvnKey {
// 			lease.RevisionVersionNumber, err = uuid.Parse(string(kv.Value))
// 			if err != nil {
// 				return nil, fmt.Errorf("failed to parse RVN: %w", err)
// 			}
// 		} else if string(kv.Key) == etcd.electionPrefix()+etcdLeaderKey {
// 			lease.Leader = string(kv.Value)
// 		} else if string(kv.Key) == etcd.electionPrefix()+etcdDurationMsKey {
// 			var durationMs int64
// 			if _, err := fmt.Sscanf(string(kv.Value), "%d", &durationMs); err != nil {
// 				return nil, fmt.Errorf("failed to parse lease duration: %w", err)
// 			}
// 			lease.Duration = time.Duration(durationMs) * time.Millisecond
// 		} else {
// 			log.Printf("WARNING: Ignoring unexpected key in election prefix: %s", kv.Key)
// 		}
// 	}
// 	if lease.RevisionVersionNumber == uuid.Nil || lease.Leader == "" || lease.Duration <= 0 {
// 		return nil, fmt.Errorf("incomplete lease data: %+v", lease)
// 	}

// 	return &lease, nil
// }
