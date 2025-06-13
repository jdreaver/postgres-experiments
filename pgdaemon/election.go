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
}

func (e *observedLease) IsExpired() bool {
	if e == nil {
		return true
	}
	return time.Since(e.seen) > e.lease.duration
}
