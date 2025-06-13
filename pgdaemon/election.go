package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
)

type Election struct {
	// nodeName is the name of this node in the election (usually
	// the hostname).
	nodeName          string
	leaseDuration     time.Duration
	lastObservedLease *observedLease
}

func NewElection(nodeName string, leaseDuration time.Duration) *Election {
	return &Election{
		nodeName:      nodeName,
		leaseDuration: leaseDuration,
	}
}

// ElectionBackend is a data store (usually a connection to one) that
// supports the primitives necessary for leader election, principally
// the ability to atomically compare-and-swap a lease.
type ElectionBackend interface {
	FetchCurrentLease(ctx context.Context) (*lease, error)
	AtomicCompareAndSwapLease(ctx context.Context, prevRVN *uuid.UUID, newLease lease) (bool, error)
}

func (e *Election) Run(ctx context.Context, backend ElectionBackend) error {
	currLease, err := backend.FetchCurrentLease(ctx)
	if err != nil {
		e.lastObservedLease = nil
		return fmt.Errorf("failed to fetch lease: %w", err)
	}

	result := evaluateElection(e.lastObservedLease, currLease, e.nodeName, time.Now())
	e.lastObservedLease = result.lease
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
		newLease := lease{
			leader:                e.nodeName,
			revisionVersionNumber: uuid.New(),
			duration:              e.leaseDuration,
		}

		var prevRVN *uuid.UUID
		if e.lastObservedLease != nil {
			prevRVN = &e.lastObservedLease.lease.revisionVersionNumber
		}

		wonElection, err := backend.AtomicCompareAndSwapLease(ctx, prevRVN, newLease)
		if err != nil {
			return fmt.Errorf("failed to run etcd election: %w", err)
		}

		if wonElection {
			log.Printf("We are the leader")
		} else {
			log.Printf("Lost CAS race to become leader")
		}
	}

	return nil
}

func (e *Election) IsLeader() bool {
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

// lease represents a time-bound lock held by the leader node. The
// leader keeps the lease updated by refreshing it. Other nodes monitor
// lease expiration by using local monotonic clocks. That means a lease
// expires leaseDurationMilliseconds after a node first observes the
// lease.
type lease struct {
	// leader is the name of the leader node that holds the lock.
	leader string

	// revisionVersionNumber (RVN) is a unique identifier that is
	// updated every time the leader refreshes its lease.
	revisionVersionNumber uuid.UUID

	duration time.Duration
}

// observedLease holds the latest lease information observed by a node,
// along with the local node time it was seen.
type observedLease struct {
	lease lease

	// N.B. Go's time.Now() includes a monotonic clock reading (see
	// https://pkg.go.dev/time#hdr-Monotonic_Clocks).
	seen time.Time

	// timeLeft is the time left for the lease to expire, calculated
	// as the difference between the lease duration and the time
	// elapsed since the lease was observed.
	timeLeft time.Duration
}

func (e *observedLease) IsExpired() bool {
	if e == nil {
		return true
	}
	return e.timeLeft <= 0
}

type electionResult struct {
	shouldRunElection bool
	lease             *observedLease
	comment           string
}

func evaluateElection(prevLease *observedLease, lease *lease, nodeName string, now time.Time) electionResult {
	// If lease is nil, it means there is no current leader and we
	// should run for election.
	if lease == nil {
		return electionResult{
			shouldRunElection: true,
			lease:             nil,
			comment:           "No current leader, running for election",
		}
	}

	// Lease is non-nil.
	result := electionResult{
		shouldRunElection: false,
		lease: &observedLease{
			lease:    *lease,
			seen:     now,
			timeLeft: lease.duration,
		},
	}

	if prevLease == nil {
		result.comment = "No previous lease"
		return result
	}

	// If we are the lease holder, we should run for election (which
	// also means refreshing the lease).
	if prevLease.lease.leader == nodeName {
		result.shouldRunElection = true
		result.comment = "We are the current lease holder, refreshing lease"
		return result
	}

	// If the RVN is the same, update the time left. Otherwise note
	// that the least is updated.
	if prevLease.lease.revisionVersionNumber == lease.revisionVersionNumber {
		result.lease.timeLeft = prevLease.timeLeft - now.Sub(prevLease.seen)
		result.comment = "No change in observed lease, updated time left"
	} else {
		result.comment = "Updated observed lease"
	}

	// If the lease is expired, we should run for election.
	if result.lease.IsExpired() {
		result.shouldRunElection = true

		// Warn if our own lease is the one that expired
		if result.lease.lease.leader == nodeName {
			result.comment = "WARNING: Our own lease expired, running for election"
		} else {
			result.comment = "Previous lease expired, running for election"
		}

		return result
	}

	return result
}
