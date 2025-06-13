package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	clientv3 "go.etcd.io/etcd/client/v3"

	"pgdaemon/election"
)

type EtcdBackend struct {
	client      *clientv3.Client
	clusterName string
	nodeName    string
}

const etcdRvnKey = "/rvn"
const etcdLeaderKey = "/leader"
const etcdDurationMsKey = "/lease_duration_ms"

func NewEtcdBackend(client *clientv3.Client, clusterName string, nodeName string) (*EtcdBackend, error) {
	return &EtcdBackend{
		clusterName: clusterName,
		client:      client,
		nodeName:    nodeName,
	}, nil
}

func (etcd *EtcdBackend) clusterPrefix() string {
	return "/" + etcd.clusterName
}

func (etcd *EtcdBackend) electionPrefix() string {
	return etcd.clusterPrefix() + "/election"
}

func (etcd *EtcdBackend) clusterSpecPrefix() string {
	return etcd.clusterPrefix() + "/spec"
}

func (etcd *EtcdBackend) nodeStatusPrefix(nodeName string) string {
	return etcd.clusterPrefix() + "/node-statuses/" + nodeName
}

func (etcd *EtcdBackend) AtomicCompareAndSwapLease(ctx context.Context, prevRVN *uuid.UUID, newLease election.Lease) (bool, error) {
	compare := clientv3.Compare(clientv3.CreateRevision(etcd.electionPrefix()+etcdRvnKey), "=", 0)
	if prevRVN != nil {
		lastRVN := *prevRVN
		compare = clientv3.Compare(clientv3.Value(etcd.electionPrefix()+etcdRvnKey), "=", lastRVN.String())
	}

	txn := etcd.client.Txn(ctx)
	txnResp, err := txn.If(
		compare,
	).Then(
		clientv3.OpPut(etcd.electionPrefix()+etcdRvnKey, newLease.RevisionVersionNumber.String()),
		clientv3.OpPut(etcd.electionPrefix()+etcdLeaderKey, newLease.Leader),
		clientv3.OpPut(etcd.electionPrefix()+etcdDurationMsKey, fmt.Sprintf("%d", newLease.Duration.Milliseconds())),
	).Commit()
	if err != nil {
		return false, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return txnResp.Succeeded, nil
}

func (etcd *EtcdBackend) FetchCurrentLease(ctx context.Context) (*election.Lease, error) {
	getResp, err := etcd.client.Get(ctx, etcd.electionPrefix(), clientv3.WithPrefix())
	if err != nil {
		return nil, fmt.Errorf("failed to get election key from etcd: %w", err)
	}

	if len(getResp.Kvs) == 0 {
		return nil, nil
	}

	var lease election.Lease
	for _, kv := range getResp.Kvs {
		if string(kv.Key) == etcd.electionPrefix()+etcdRvnKey {
			lease.RevisionVersionNumber, err = uuid.Parse(string(kv.Value))
			if err != nil {
				return nil, fmt.Errorf("failed to parse RVN: %w", err)
			}
		} else if string(kv.Key) == etcd.electionPrefix()+etcdLeaderKey {
			lease.Leader = string(kv.Value)
		} else if string(kv.Key) == etcd.electionPrefix()+etcdDurationMsKey {
			var durationMs int64
			if _, err := fmt.Sscanf(string(kv.Value), "%d", &durationMs); err != nil {
				return nil, fmt.Errorf("failed to parse lease duration: %w", err)
			}
			lease.Duration = time.Duration(durationMs) * time.Millisecond
		} else {
			log.Printf("WARNING: Ignoring unexpected key in election prefix: %s", kv.Key)
		}
	}
	if lease.RevisionVersionNumber == uuid.Nil || lease.Leader == "" || lease.Duration <= 0 {
		return nil, fmt.Errorf("incomplete lease data: %+v", lease)
	}

	return &lease, nil
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

func (etcd *EtcdBackend) InitializeCluster(ctx context.Context, spec *ClusterSpec) error {
	if spec.PrimaryName == "" {
		return fmt.Errorf("primary name cannot be empty")
	}

	specBytes, err := json.Marshal(spec)
	if err != nil {
		return fmt.Errorf("failed to marshal cluster spec: %w", err)
	}

	_, err = etcd.client.Put(ctx, etcd.clusterSpecPrefix(), string(specBytes))
	if err != nil {
		return fmt.Errorf("failed to write cluster spec to etcd: %w", err)
	}

	log.Printf("Cluster spec initialized: %s", string(specBytes))

	return nil
}

func (etcd *EtcdBackend) FetchClusterSpec(ctx context.Context) (*ClusterSpec, error) {
	resp, err := etcd.client.Get(ctx, etcd.clusterSpecPrefix())
	if err != nil {
		return nil, fmt.Errorf("failed to fetch cluster spec: %w", err)
	}

	if len(resp.Kvs) == 0 {
		return nil, fmt.Errorf("cluster spec not found")
	}

	var spec ClusterSpec
	if err := json.Unmarshal(resp.Kvs[0].Value, &spec); err != nil {
		return nil, fmt.Errorf("failed to unmarshal cluster spec: %w", err)
	}

	return &spec, nil
}
