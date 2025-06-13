package election

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestEvaluateElection_NoLease(t *testing.T) {
	now := time.Now()
	result := evaluateElection(nil, nil, "nodeA", now)
	assert.True(t, result.shouldRunElection, "expected to run election when no lease exists")
	assert.Equal(t, "No current leader, running for election", result.comment, "expected comment when no lease exists")
}

func TestEvaluateElection_NoPreviousLease(t *testing.T) {
	now := time.Now()
	lease := &Lease{
		Leader:                "nodeA",
		RevisionVersionNumber: uuid.New(),
		Duration:              10 * time.Second,
	}

	result := evaluateElection(nil, lease, "nodeA", now)
	assert.False(t, result.shouldRunElection, "expected to do nothing when no previous lease exists")
	assert.Equal(t, "Seeing first lease, doing nothing", result.comment, "expected comment when no previous lease exists")
	assert.NotNil(t, result.lease, "expected a new observed lease to be created")
}

func TestEvaluateElection_LeaseHolder(t *testing.T) {
	now := time.Now()
	lease := &Lease{
		Leader:                "nodeA",
		RevisionVersionNumber: uuid.New(),
		Duration:              10 * time.Second,
	}

	prevLease := &observedLease{
		lease:    *lease,
		seen:     now.Add(-5 * time.Second),
		timeLeft: 5 * time.Second,
	}

	result := evaluateElection(prevLease, lease, "nodeA", now)
	assert.True(t, result.shouldRunElection, "expected to run election when we are the lease holder")
	assert.Equal(t, "We are the current lease holder, refreshing lease", result.comment, "expected comment for lease holder")
	assert.NotNil(t, result.lease, "expected a new observed lease to be created")
}

func TestEvaluateElection_LeaseNotHolder(t *testing.T) {
	now := time.Now()
	lease := &Lease{
		Leader:                "nodeB",
		RevisionVersionNumber: uuid.New(),
		Duration:              10 * time.Second,
	}

	prevLease := &observedLease{
		lease:    *lease,
		seen:     now.Add(-1 * time.Second),
		timeLeft: 5 * time.Second,
	}

	result := evaluateElection(prevLease, lease, "nodeA", now)
	assert.False(t, result.shouldRunElection, "expected not to run election when we are not the lease holder")
	assert.Equal(t, "No change in observed lease, updated time left", result.comment, "expected comment for updated lease")
	assert.Equal(t, 5*time.Second-now.Sub(prevLease.seen), result.lease.timeLeft, "expected updated time left")
	assert.NotNil(t, result.lease, "expected a new observed lease to be created")
}

func TestEvaluateElection_LeaseExpired(t *testing.T) {
	now := time.Now()
	lease := &Lease{
		Leader:                "nodeB",
		RevisionVersionNumber: uuid.New(),
		Duration:              1 * time.Second, // Short duration to simulate expiration
	}

	prevLease := &observedLease{
		lease:    *lease,
		seen:     now.Add(-2 * time.Second), // Simulate that the lease was seen 2 seconds ago
		timeLeft: 1 * time.Second,
	}

	result := evaluateElection(prevLease, lease, "nodeA", now)
	assert.True(t, result.shouldRunElection, "expected to run election when lease is expired")
	assert.Equal(t, "Previous lease expired, running for election", result.comment, "expected comment for updated lease")
	assert.NotNil(t, result.lease, "expected a new observed lease to be created")
}
