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

// lease holds the lease information data that is stored in a database.
type lease struct {
	// leader is the name of the leader node that holds the lock.
	Leader string `json:"leader"`

	// revisionVersionNumber (RVN) is a unique identifier that is
	// updated every time the leader refreshes its lease.
	RevisionVersionNumber uuid.UUID `json:"rvn"`

	// leaseDurationMilliseconds is the duration of the lease. A local,
	// monotonic clock is used to determine if the lease has expired
	// or not.
	LeaseDurationMilliseconds int64 `json:"lease_duration_ms"`
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
	lockDuration := time.Duration(e.lease.LeaseDurationMilliseconds) * time.Millisecond
	return time.Since(e.seen) > lockDuration
}

type EtcdElection struct {
	// electionPrefix is the prefix for the election key in etcd.
	electionPrefix string

	// nodeName is the name of this node in the election (usually
	// the hostname).
	nodeName string

	etcdHost          string
	etcdPort          string
	leaseDuration     time.Duration
	lastObservedLease *observedLease
}

// TODO: Too many string arguments
func NewEtcdElection(electionPrefix string, etcdHost string, etcdPort string, nodeName string, leaseDuration time.Duration) *EtcdElection {
	return &EtcdElection{
		electionPrefix: electionPrefix,
		etcdHost:       etcdHost,
		etcdPort:       etcdPort,
		nodeName:       nodeName,
		leaseDuration:  leaseDuration,
	}
}

func (etcd *EtcdElection) RunElection(ctx context.Context) error {
	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{fmt.Sprintf("%s:%s", etcd.etcdHost, etcd.etcdPort)},
		DialTimeout: 2 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("failed to connect to etcd: %w", err)
	}
	defer cli.Close()

	err = etcd.updateObservedLease(ctx, *cli)
	if err != nil {
		return fmt.Errorf("failed to update observed lease: %w", err)
	}

	// If the lease has expired (or there is no lease), try to
	// become the leader. If we are the leader, update the lease
	// anyway to get a new RVN.
	if etcd.lastObservedLease == nil || etcd.lastObservedLease.IsExpired() || etcd.lastObservedLease.lease.Leader == etcd.nodeName {
		// Warn if we are the current lease holder
		if etcd.lastObservedLease != nil && etcd.lastObservedLease.IsExpired() && etcd.lastObservedLease.lease.Leader == etcd.nodeName {
			log.Printf("WARNING: Our own lease has expired!")
		}

		newRVN := uuid.New()
		newLease := lease{
			Leader:                    etcd.nodeName,
			RevisionVersionNumber:     newRVN,
			LeaseDurationMilliseconds: etcd.leaseDuration.Milliseconds(),
		}

		newLeaseBytes, err := json.Marshal(newLease)
		if err != nil {
			return fmt.Errorf("failed to marshal lease data: %w. Raw data: %+v", err, newLease)
		}

		// By default, assume previous lease doesn't exist
		compare := clientv3.Compare(clientv3.Version(etcd.electionPrefix), "=", 0)
		if etcd.lastObservedLease != nil {
			prevLeaseBytes, err := json.Marshal(etcd.lastObservedLease.lease)
			if err != nil {
				return fmt.Errorf("failed to marshal previous lease data: %w. Raw data: %+v", err, etcd.lastObservedLease.lease)
			}

			compare = clientv3.Compare(clientv3.Value(etcd.electionPrefix), "=", string(prevLeaseBytes))
		}

		electionKey := etcd.electionPrefix
		txn := cli.Txn(ctx)
		txnResp, err := txn.If(
			compare,
		).Then(
			clientv3.OpPut(electionKey, string(newLeaseBytes)),
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

func (etcd *EtcdElection) updateObservedLease(ctx context.Context, cli clientv3.Client) error {
	lease, err := etcd.fetchLease(ctx, cli)
	if err != nil {
		etcd.lastObservedLease = nil
		return fmt.Errorf("failed to fetch lease: %w", err)
	}

	// If lease is nil, it means there is no current leader
	if lease == nil {
		etcd.lastObservedLease = nil
		return nil
	}

	leaseDuration := time.Duration(lease.LeaseDurationMilliseconds) * time.Millisecond

	// The lease is non-nil. If it different from the last observed
	// lease, updated the last observed lease.
	if etcd.lastObservedLease == nil || lease.RevisionVersionNumber != etcd.lastObservedLease.lease.RevisionVersionNumber {
		etcd.lastObservedLease = &observedLease{
			lease: *lease,
			seen:  time.Now(),
		}
		log.Printf(
			"Updated observed lease. leader: %s, rvn: %s, duration: %s",
			lease.Leader,
			lease.RevisionVersionNumber,
			leaseDuration,
		)
		return nil
	}

	timeLeftInLease := time.Duration(lease.LeaseDurationMilliseconds) * time.Millisecond
	if etcd.lastObservedLease != nil {
		timeLeftInLease -= time.Since(etcd.lastObservedLease.seen)
	}

	log.Printf(
		"No change in observed lease. leader: %s, rvn: %s, duration: %s, remaining time: %s\n",
		lease.Leader,
		lease.RevisionVersionNumber,
		leaseDuration,
		timeLeftInLease,
	)
	return nil
}

func (etcd *EtcdElection) fetchLease(ctx context.Context, cli clientv3.Client) (*lease, error) {
	getResp, err := cli.Get(ctx, etcd.electionPrefix)
	if err != nil {
		return nil, fmt.Errorf("failed to get election key from etcd: %w", err)
	}

	if len(getResp.Kvs) == 0 {
		return nil, nil
	}

	var lease lease
	err = json.Unmarshal(getResp.Kvs[0].Value, &lease)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal lease data: %w. Raw data: %+v", err, getResp.Kvs[0].Value)
	}

	return &lease, nil
}

func (e *EtcdElection) IsLeader() bool {
	if e.lastObservedLease == nil {
		return false
	}
	if e.lastObservedLease.lease.Leader != e.nodeName {
		return false
	}
	leaseDuration := time.Duration(e.lastObservedLease.lease.LeaseDurationMilliseconds) * time.Millisecond
	if time.Since(e.lastObservedLease.seen) > leaseDuration {
		return false
	}
	return true
}
