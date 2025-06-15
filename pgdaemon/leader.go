package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"pgdaemon/election"
)

// leaderReconcilerLoop runs the leader election and performs leader tasks.
func leaderReconcilerLoop(ctx context.Context, store StateStore, conf config) error {
	election, err := election.New(conf.nodeName, conf.leaseDuration)
	if err != nil {
		return fmt.Errorf("failed to create election: %w", err)
	}

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("returning ctx.Done() error in leader loop: %w", ctx.Err())
		case <-ticker.C:
			eCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			err := election.Run(eCtx, store)
			cancel()
			if err != nil {
				log.Printf("Election error: %v", err)
			}

			if election.IsLeader() {
				if err := performLeaderTasks(ctx, store); err != nil {
					log.Printf("Failed to perform leader tasks: %v", err)
				}
			}
		}
	}
}

func performLeaderTasks(ctx context.Context, store StateStore) error {
	state, err := store.FetchClusterState(ctx)
	if err != nil {
		return fmt.Errorf("Failed to fetch cluster spec: %w", err)
	}

	log.Printf("Cluster state: %+v", state)

	return nil
}
