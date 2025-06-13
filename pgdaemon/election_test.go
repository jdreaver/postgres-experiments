package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestEvaluateElection_NoLease(t *testing.T) {
	now := time.Now()
	result := evaluateElection(nil, nil, "nodeA", now)
	assert.True(t, result.shouldRunElection, "expected to run election when no lease exists")
	assert.Equal(t, result.comment, "No current leader, running for election", "expected comment when no lease exists")
}
