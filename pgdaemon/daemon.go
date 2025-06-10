package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"golang.org/x/sync/errgroup"
)

func daemon(ctx context.Context, store StateStore, conf config) {
	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		return leaderReconcilerLoop(ctx, store)
	})

	g.Go(func() error {
		return nodeReconcilerLoop(ctx, store, conf)
	})

	g.Go(func() error {
		return runHealthCheckServer(ctx, conf)
	})

	if err := g.Wait(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Fatal error: %v", err)
	}
}

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

func performLeaderTasks(ctx context.Context, store StateStore) error {
	clusterState, err := store.FetchClusterDesiredState(ctx)
	if err != nil {
		return fmt.Errorf("Failed to fetch cluster desired state: %w", err)
	}

	log.Printf("Cluster desired state: %+v", clusterState)
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

func runHealthCheckServer(ctx context.Context, conf config) error {
	srv := &http.Server{
		Addr: conf.listenAddress,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			timeout := 500 * time.Millisecond
			pgOK, pgErr := checkDB(conf.postgresHost, conf.postgresPort, conf.postgresUser, timeout)
			pbOK, pbErr := checkDB(conf.pgBouncerHost, conf.pgBouncerPort, conf.postgresUser, timeout)

			resp := HealthResponse{
				PostgresOK:   pgOK,
				PostgresErr:  pgErr.Error(),
				PgBouncerOK:  pbOK,
				PgBouncerErr: pbErr.Error(),
			}

			status := http.StatusOK
			if !pgOK || !pbOK {
				status = http.StatusServiceUnavailable
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			json.NewEncoder(w).Encode(resp)
		}),
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx) // graceful shutdown
	}()

	log.Printf("Listening on %s", srv.Addr)
	return srv.ListenAndServe()
}
