package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	clientv3 "go.etcd.io/etcd/client/v3"
)

type EtcdBackend struct {
	clusterName string

	// nodeName is the name of this node in the election (usually
	// the hostname).
	nodeName string

	client            *clientv3.Client
	leaseDuration     time.Duration
	lastObservedLease *observedLease
}

const etcdRvnKey = "/rvn"
const etcdLeaderKey = "/leader"
const etcdDurationMsKey = "/lease_duration_ms"

// TODO: Too many string arguments
func NewEtcdBackend(client *clientv3.Client, clusterName string, nodeName string, leaseDuration time.Duration) (*EtcdBackend, error) {
	return &EtcdBackend{
		clusterName:   clusterName,
		client:        client,
		nodeName:      nodeName,
		leaseDuration: leaseDuration,
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

func (etcd *EtcdBackend) RunElection(ctx context.Context) error {
	lease, err := etcd.fetchLease(ctx)
	if err != nil {
		etcd.lastObservedLease = nil
		return fmt.Errorf("failed to fetch lease: %w", err)
	}

	result := evaluateElection(etcd.lastObservedLease, lease, etcd.nodeName, time.Now())
	etcd.lastObservedLease = result.lease
	if result.lease != nil {
		log.Printf(
			"Lease: leader: %s, rvn: %s, duration: %s, time left: %s",
			result.lease.lease.leader,
			result.lease.lease.revisionVersionNumber,
			result.lease.lease.duration,
			result.lease.timeLeft,
		)
	}
	if result.comment != "" {
		log.Printf("Election evaluation: %s", result.comment)
	}

	if result.shouldRunElection {
		newRVN := uuid.New()

		// By default, assume previous lease doesn't exist
		compare := clientv3.Compare(clientv3.CreateRevision(etcd.electionPrefix()+etcdRvnKey), "=", 0)
		if etcd.lastObservedLease != nil {
			lastRVN := etcd.lastObservedLease.lease.revisionVersionNumber
			compare = clientv3.Compare(clientv3.Value(etcd.electionPrefix()+etcdRvnKey), "=", lastRVN.String())
		}

		txn := etcd.client.Txn(ctx)
		txnResp, err := txn.If(
			compare,
		).Then(
			clientv3.OpPut(etcd.electionPrefix()+etcdRvnKey, newRVN.String()),
			clientv3.OpPut(etcd.electionPrefix()+etcdLeaderKey, etcd.nodeName),
			clientv3.OpPut(etcd.electionPrefix()+etcdDurationMsKey, fmt.Sprintf("%d", etcd.leaseDuration.Milliseconds())),
		).Commit()
		if err != nil {
			return fmt.Errorf("failed to commit transaction: %w", err)
		}

		if txnResp.Succeeded {
			log.Printf("We are the leader. New RVN: %s", newRVN)
		} else {
			log.Printf("Lost CAS race to become leader")
		}
	}

	return nil
}

func (etcd *EtcdBackend) fetchLease(ctx context.Context) (*lease, error) {
	getResp, err := etcd.client.Get(ctx, etcd.electionPrefix(), clientv3.WithPrefix())
	if err != nil {
		return nil, fmt.Errorf("failed to get election key from etcd: %w", err)
	}

	if len(getResp.Kvs) == 0 {
		return nil, nil
	}

	var lease lease
	for _, kv := range getResp.Kvs {
		if string(kv.Key) == etcd.electionPrefix()+etcdRvnKey {
			lease.revisionVersionNumber, err = uuid.Parse(string(kv.Value))
			if err != nil {
				return nil, fmt.Errorf("failed to parse RVN: %w", err)
			}
		} else if string(kv.Key) == etcd.electionPrefix()+etcdLeaderKey {
			lease.leader = string(kv.Value)
		} else if string(kv.Key) == etcd.electionPrefix()+etcdDurationMsKey {
			var durationMs int64
			if _, err := fmt.Sscanf(string(kv.Value), "%d", &durationMs); err != nil {
				return nil, fmt.Errorf("failed to parse lease duration: %w", err)
			}
			lease.duration = time.Duration(durationMs) * time.Millisecond
		} else {
			log.Printf("WARNING: Ignoring unexpected key in election prefix: %s", kv.Key)
		}
	}
	if lease.revisionVersionNumber == uuid.Nil || lease.leader == "" || lease.duration <= 0 {
		return nil, fmt.Errorf("incomplete lease data: %+v", lease)
	}

	return &lease, nil
}

func (e *EtcdBackend) IsLeader() bool {
	if e.lastObservedLease == nil {
		return false
	}
	if e.lastObservedLease.lease.leader != e.nodeName {
		return false
	}
	if time.Since(e.lastObservedLease.seen) > e.lastObservedLease.lease.duration {
		return false
	}
	return true
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
