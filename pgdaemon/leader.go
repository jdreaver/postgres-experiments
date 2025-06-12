package main

import (
	"context"
	"fmt"
	"log"
	"time"
)

// leaderReconcilerLoop runs the leader election and performs leader tasks.
func leaderReconcilerLoop(ctx context.Context, store StateStore, conf config) error {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("returning ctx.Done() error in leader loop: %w", ctx.Err())
		case <-ticker.C:
			eCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			err := store.RunElection(eCtx)
			cancel()
			if err != nil {
				log.Printf("Election error: %v", err)
			}

			if store.IsLeader() {
				log.Printf("I'm the leader")
				if err := performLeaderTasks(ctx, store, conf); err != nil {
					log.Printf("Failed to perform leader tasks: %v", err)
				}
			}
		}
	}
}

func performLeaderTasks(ctx context.Context, store StateStore, conf config) error {
	clusterState, err := store.FetchClusterDesiredState(ctx)
	if err != nil {
		return fmt.Errorf("Failed to fetch cluster desired state: %w", err)
	}

	log.Printf("Cluster desired state: %+v", clusterState)

	// Fetch observed states of all nodes to check cluster health
	observedStates, err := store.FetchAllNodeObservedStates(ctx)
	if err != nil {
		return fmt.Errorf("Failed to fetch observed states: %w", err)
	}

	// Check if current primary is healthy
	needsFailover := false
	currentPrimary := clusterState.PrimaryName
	
	if primaryState, exists := observedStates[currentPrimary]; exists {
		if primaryState.Error != nil {
			log.Printf("Primary %s has error: %s - initiating failover", currentPrimary, *primaryState.Error)
			needsFailover = true
		} else if primaryState.IsPrimary != nil && !*primaryState.IsPrimary {
			log.Printf("Primary %s is not actually primary - initiating failover", currentPrimary)
			needsFailover = true
		}
	} else {
		log.Printf("Primary %s has no observed state - initiating failover", currentPrimary)
		needsFailover = true
	}

	// If failover is needed, select new primary
	if needsFailover {
		newPrimary, err := performFailover(ctx, store, clusterState, observedStates, conf.failoverTimeout)
		if err != nil {
			log.Printf("Failover failed: %v", err)
			// Continue with existing primary for now
		} else {
			// Update cluster desired state with new primary
			clusterState.PrimaryName = newPrimary
			
			// Move old primary to replica list if it's not already there
			foundOldPrimary := false
			for _, replica := range clusterState.ReplicaNames {
				if replica == currentPrimary {
					foundOldPrimary = true
					break
				}
			}
			if !foundOldPrimary && currentPrimary != newPrimary {
				clusterState.ReplicaNames = append(clusterState.ReplicaNames, currentPrimary)
			}

			// Remove new primary from replica list
			newReplicaNames := make([]string, 0, len(clusterState.ReplicaNames))
			for _, replica := range clusterState.ReplicaNames {
				if replica != newPrimary {
					newReplicaNames = append(newReplicaNames, replica)
				}
			}
			clusterState.ReplicaNames = newReplicaNames

			// Update cluster desired state in store
			if err := store.InitializeCluster(ctx, clusterState); err != nil {
				log.Printf("Failed to update cluster state after failover: %v", err)
			} else {
				log.Printf("Failover complete: new primary is %s", newPrimary)
			}
		}
	}

	// Set desired state for all nodes
	nodeDesiredState := NodeDesiredState{
		PrimaryName: clusterState.PrimaryName,
	}

	if err := store.SetNodeDesiredState(ctx, clusterState.PrimaryName, &nodeDesiredState); err != nil {
		return fmt.Errorf("Failed to set primary desired state: %w", err)
	}

	for _, replica := range clusterState.ReplicaNames {
		if err := store.SetNodeDesiredState(ctx, replica, &nodeDesiredState); err != nil {
			return fmt.Errorf("Failed to set node desired state for replica %s: %w", replica, err)
		}
	}

	return nil
}

func performFailover(ctx context.Context, store StateStore, clusterState *ClusterDesiredState, observedStates map[string]*NodeObservedState, catchupTimeout time.Duration) (string, error) {
	log.Printf("Starting failover process - current primary: %s", clusterState.PrimaryName)

	// Find the best replica to promote
	bestReplica, err := findBestReplica(observedStates, clusterState.PrimaryName)
	if err != nil {
		return "", fmt.Errorf("failed to find suitable replica: %w", err)
	}

	log.Printf("Selected %s as new primary", bestReplica)
	
	// Wait for replicas to catch up to the best replica's LSN
	if err := waitForReplicaCatchup(ctx, store, bestReplica, observedStates, catchupTimeout); err != nil {
		log.Printf("Replica catch-up failed or timed out: %v", err)
		// Continue with failover anyway
	}

	return bestReplica, nil
}

func waitForReplicaCatchup(ctx context.Context, store StateStore, targetReplica string, initialStates map[string]*NodeObservedState, timeout time.Duration) error {
	targetState, exists := initialStates[targetReplica]
	if !exists || targetState.PgStatWalReceiver == nil || targetState.PgStatWalReceiver.WrittenLsn == nil {
		return fmt.Errorf("target replica %s has no LSN information", targetReplica)
	}

	targetLSN, err := parseLSN(*targetState.PgStatWalReceiver.WrittenLsn)
	if err != nil {
		return fmt.Errorf("failed to parse target LSN: %w", err)
	}

	log.Printf("Waiting up to %v for replicas to catch up to LSN %s", timeout, *targetState.PgStatWalReceiver.WrittenLsn)

	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				log.Printf("Replica catch-up timeout exceeded")
				return nil // Not an error, just timeout
			}

			// Check current state of all replicas
			currentStates, err := store.FetchAllNodeObservedStates(ctx)
			if err != nil {
				log.Printf("Failed to fetch current states during catch-up: %v", err)
				continue
			}

			allCaughtUp := true
			for nodeName, state := range currentStates {
				// Skip the target replica and any primaries
				if nodeName == targetReplica {
					continue
				}
				if state.IsPrimary != nil && *state.IsPrimary {
					continue
				}
				if state.Error != nil {
					continue // Skip nodes with errors
				}
				if state.PgStatWalReceiver == nil || state.PgStatWalReceiver.WrittenLsn == nil {
					continue // Skip nodes without replication info
				}

				replicaLSN, err := parseLSN(*state.PgStatWalReceiver.WrittenLsn)
				if err != nil {
					log.Printf("Failed to parse LSN for replica %s: %v", nodeName, err)
					continue
				}

				if replicaLSN < targetLSN {
					log.Printf("Replica %s still catching up: %s < %s", nodeName, *state.PgStatWalReceiver.WrittenLsn, *targetState.PgStatWalReceiver.WrittenLsn)
					allCaughtUp = false
				}
			}

			if allCaughtUp {
				log.Printf("All replicas have caught up")
				return nil
			}
		}
	}
}
