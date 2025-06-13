package main

import (
	"context"
	"fmt"
	"log"
	"time"
)

// leaderReconcilerLoop runs the leader election and performs leader tasks.
func leaderReconcilerLoop(ctx context.Context, store StateStore) error {
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
				if err := performLeaderTasks(ctx, store); err != nil {
					log.Printf("Failed to perform leader tasks: %v", err)
				}
			}
		}
	}
}

func performLeaderTasks(ctx context.Context, store StateStore) error {
	clusterSpec, err := store.FetchClusterSpec(ctx)
	if err != nil {
		return fmt.Errorf("Failed to fetch cluster spec: %w", err)
	}

	log.Printf("Cluster spec: %+v", clusterSpec)

	return nil
}
