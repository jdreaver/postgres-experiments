package election

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

func New(nodeName string, leaseDuration time.Duration) (*Election, error) {
	if leaseDuration <= 0 {
		return nil, fmt.Errorf("lease duration must be greater than zero")
	}

	return &Election{
		nodeName:      nodeName,
		leaseDuration: leaseDuration,
	}, nil
}

// ElectionBackend is a data store (usually a connection to one) that
// supports the primitives necessary for leader election, principally
// the ability to atomically compare-and-swap a lease.
type ElectionBackend interface {
	FetchCurrentLease(ctx context.Context) (*Lease, error)
	AtomicCompareAndSwapLease(ctx context.Context, prevRVN *uuid.UUID, newLease Lease) (bool, error)
}

func (e *Election) Run(ctx context.Context, backend ElectionBackend) error {
	makeUuid := func() uuid.UUID {
		return uuid.New()
	}

	return e.runInner(ctx, backend, time.Now(), makeUuid)
}

func (e *Election) runInner(ctx context.Context, backend ElectionBackend, now time.Time, makeUuid func() uuid.UUID) error {
	currLease, err := backend.FetchCurrentLease(ctx)
	if err != nil {
		e.lastObservedLease = nil
		return fmt.Errorf("failed to fetch lease: %w", err)
	}

	result := evaluateElection(e.lastObservedLease, currLease, e.nodeName, now)
	e.lastObservedLease = result.lease
	if result.lease != nil {
		log.Printf(
			"Lease: leader: %s, rvn: %s, duration: %s, time left: %s",
			result.lease.lease.Leader,
			result.lease.lease.RevisionVersionNumber,
			result.lease.lease.Duration,
			result.lease.timeLeft,
		)
	}
	if result.comment != "" {
		log.Printf("Election evaluation: %s", result.comment)
	}

	if result.shouldRunElection {
		newLease := Lease{
			Leader:                e.nodeName,
			RevisionVersionNumber: makeUuid(),
			Duration:              e.leaseDuration,
		}

		var prevRVN *uuid.UUID
		if e.lastObservedLease != nil {
			prevRVN = &e.lastObservedLease.lease.RevisionVersionNumber
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
	if e.lastObservedLease.lease.Leader != e.nodeName {
		return false
	}
	if time.Since(e.lastObservedLease.seen) > e.lastObservedLease.lease.Duration {
		return false
	}
	return true
}

// Lease represents a time-bound lock held by the leader node. The
// leader keeps the Lease updated by refreshing it. Other nodes monitor
// Lease expiration by using local monotonic clocks. That means a Lease
// expires leaseDurationMilliseconds after a node first observes the
// Lease.
type Lease struct {
	// Leader is the name of the Leader node that holds the lock.
	Leader string

	// RevisionVersionNumber (RVN) is a unique identifier that is
	// updated every time the leader refreshes its lease.
	RevisionVersionNumber uuid.UUID

	Duration time.Duration
}

// observedLease holds the latest lease information observed by a node,
// along with the local node time it was seen.
type observedLease struct {
	lease Lease

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

func evaluateElection(prevLease *observedLease, lease *Lease, nodeName string, now time.Time) electionResult {
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
			timeLeft: lease.Duration,
		},
	}

	if prevLease == nil {
		result.comment = "Seeing first lease, doing nothing"
		return result
	}

	// If we are the lease holder, we should run for election (which
	// also means refreshing the lease).
	if prevLease.lease.Leader == nodeName {
		result.shouldRunElection = true
		result.comment = "We are the current lease holder, refreshing lease"
		return result
	}

	// If the RVN is the same, update the time left. Otherwise note
	// that the least is updated.
	if prevLease.lease.RevisionVersionNumber == lease.RevisionVersionNumber {
		result.lease.timeLeft = prevLease.timeLeft - now.Sub(prevLease.seen)
		result.comment = "No change in observed lease, updated time left"
	} else {
		result.comment = "Updated observed lease"
	}

	// If the lease is expired, we should run for election.
	if result.lease.IsExpired() {
		result.shouldRunElection = true

		// Warn if our own lease is the one that expired
		if result.lease.lease.Leader == nodeName {
			result.comment = "WARNING: Our own lease expired, running for election"
		} else {
			result.comment = "Previous lease expired, running for election"
		}

		return result
	}

	return result
}
