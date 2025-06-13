package main

import (
	"time"

	"github.com/google/uuid"
)

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
