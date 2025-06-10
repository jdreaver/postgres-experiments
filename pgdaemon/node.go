package main

import (
	"context"
	"fmt"
	"log"
	"time"
)

// nodeReconcilerLoop runs the node reconciler, which fetches the
// desired and observed state of the current node and performs tasks
// to reconcile that state.
func nodeReconcilerLoop(ctx context.Context, store StateStore, conf config) error {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("returning ctx.Done() error in node reconciler loop: %w", ctx.Err())
		case <-ticker.C:
			if err := storePostgresStateInStore(ctx, store, conf); err != nil {
				log.Printf("Failed to store Postgres state: %v", err)
			}

			if err := performNodeTasks(ctx, store, conf); err != nil {
				log.Printf("Failed to perform node tasks: %v", err)
			}
		}
	}
}

func storePostgresStateInStore(ctx context.Context, store StateStore, conf config) error {
	state, err := fetchPostgresNodeState(conf.postgresHost, conf.postgresPort, conf.postgresUser, 500*time.Millisecond)
	if err != nil {
		log.Printf("Failed to fetch Postgres node state: %v", err)
		errStr := err.Error()
		state = &PostgresNodeState{Error: &errStr}
	}

	wCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
	err = store.WriteCurrentNodeObservedState(wCtx, state)
	cancel()
	if err != nil {
		return fmt.Errorf("failed to write node state to store: %w", err)
	}

	return nil
}

func performNodeTasks(ctx context.Context, store StateStore, conf config) error {
	desiredState, err := store.FetchCurrentNodeDesiredState(ctx)
	if err != nil {
		return fmt.Errorf("Failed to fetch node desired state: %w", err)
	}

	log.Printf("Node desired state for %s: %+v", conf.nodeName, desiredState)

	if desiredState.PrimaryName == conf.nodeName {
		err = configureAsPrimary()
		if err != nil {
			return fmt.Errorf("Failed to configure as primary: %w", err)
		}
	} else {
		err = configureAsReplica(desiredState.PrimaryName, conf.postgresPort, conf.postgresUser)
		if err != nil {
			return fmt.Errorf("Failed to configure as replica: %w", err)
		}
	}

	if err := ensurePostgresRunning(); err != nil {
		return fmt.Errorf("Failed to ensure Postgres is running: %w", err)
	}

	if err := ensurePgBouncerRunning(); err != nil {
		return fmt.Errorf("Failed to ensure PgBouncer is running: %w", err)
	}

	return nil
}
