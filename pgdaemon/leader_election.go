package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	clientv3 "go.etcd.io/etcd/client/v3"
)

// lease holds the lease information data that is stored in a database.
type lease struct {
	// leader is the name of the leader node that holds the lock.
	leader string

	// revisionVersionNumber (RVN) is a unique identifier that is
	// updated every time the leader refreshes its lease.
	revisionVersionNumber uuid.UUID

	// leaseDurationMilliseconds is the duration of the lease. A local,
	// monotonic clock is used to determine if the lease has expired
	// or not.
	leaseDurationMilliseconds int64
}

// observedLease holds the latest lock we have observed and when we observed it.
type observedLease struct {
	lease lease

	// N.B. Go's time.Now() includes a monotonic clock reading (see
	// https://pkg.go.dev/time#hdr-Monotonic_Clocks).
	seen time.Time
}

func (e *observedLease) IsExpired() bool {
	if e == nil {
		return true
	}
	lockDuration := time.Duration(e.lease.leaseDurationMilliseconds) * time.Millisecond
	return time.Since(e.seen) > lockDuration
}

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
const etcdDurationKey = "/lease_duration_ms"

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

func (etcd *EtcdBackend) RunElection(ctx context.Context) error {
	err := etcd.updateObservedLease(ctx)
	if err != nil {
		return fmt.Errorf("failed to update observed lease: %w", err)
	}

	// If the lease has expired (or there is no lease), try to
	// become the leader. If we are the leader, update the lease
	// anyway to get a new RVN.
	if etcd.lastObservedLease == nil || etcd.lastObservedLease.IsExpired() || etcd.lastObservedLease.lease.leader == etcd.nodeName {
		// Warn if we are the current lease holder
		if etcd.lastObservedLease != nil && etcd.lastObservedLease.IsExpired() && etcd.lastObservedLease.lease.leader == etcd.nodeName {
			log.Printf("WARNING: Our own lease has expired!")
		}

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
			clientv3.OpPut(etcd.electionPrefix()+etcdDurationKey, fmt.Sprintf("%d", etcd.leaseDuration.Milliseconds())),
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

func (etcd *EtcdBackend) updateObservedLease(ctx context.Context) error {
	lease, err := etcd.fetchLease(ctx)
	if err != nil {
		etcd.lastObservedLease = nil
		return fmt.Errorf("failed to fetch lease: %w", err)
	}

	// If lease is nil, it means there is no current leader
	if lease == nil {
		etcd.lastObservedLease = nil
		return nil
	}

	leaseDuration := time.Duration(lease.leaseDurationMilliseconds) * time.Millisecond

	// The lease is non-nil. If it different from the last observed
	// lease, updated the last observed lease.
	if etcd.lastObservedLease == nil || lease.revisionVersionNumber != etcd.lastObservedLease.lease.revisionVersionNumber {
		etcd.lastObservedLease = &observedLease{
			lease: *lease,
			seen:  time.Now(),
		}
		log.Printf(
			"Updated observed lease. leader: %s, rvn: %s, duration: %s",
			lease.leader,
			lease.revisionVersionNumber,
			leaseDuration,
		)
		return nil
	}

	timeLeftInLease := time.Duration(lease.leaseDurationMilliseconds) * time.Millisecond
	if etcd.lastObservedLease != nil {
		timeLeftInLease -= time.Since(etcd.lastObservedLease.seen)
	}

	log.Printf(
		"No change in observed lease. leader: %s, rvn: %s, duration: %s, remaining time: %s\n",
		lease.leader,
		lease.revisionVersionNumber,
		leaseDuration,
		timeLeftInLease,
	)
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
		} else if string(kv.Key) == etcd.electionPrefix()+etcdDurationKey {
			var duration int64
			_, err := fmt.Sscanf(string(kv.Value), "%d", &duration)
			if err != nil {
				return nil, fmt.Errorf("failed to parse lease duration: %w", err)
			}
			lease.leaseDurationMilliseconds = duration
		} else {
			log.Printf("WARNING: Ignoring unexpected key in election prefix: %s", kv.Key)
		}
	}
	if lease.revisionVersionNumber == uuid.Nil || lease.leader == "" || lease.leaseDurationMilliseconds <= 0 {
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
	leaseDuration := time.Duration(e.lastObservedLease.lease.leaseDurationMilliseconds) * time.Millisecond
	if time.Since(e.lastObservedLease.seen) > leaseDuration {
		return false
	}
	return true
}
