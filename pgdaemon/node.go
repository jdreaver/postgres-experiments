package main

import (
	"context"
	"fmt"
	"log"
	"slices"
	"time"
)

// nodeReconcilerLoop runs the node reconciler, which fetches the spec
// and status of the current node and performs tasks to reconcile them.
func nodeReconcilerLoop(ctx context.Context, store StateStore, conf config, pgNode *PostgresNode) error {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("returning ctx.Done() error in node reconciler loop: %w", ctx.Err())
		case <-ticker.C:
			if err := storeNodeStatus(ctx, store, pgNode); err != nil {
				log.Printf("Failed to store node status: %v", err)
			}

			if err := performNodeTasks(ctx, store, conf, pgNode); err != nil {
				log.Printf("Failed to perform node tasks: %v", err)
			}
		}
	}
}

func storeNodeStatus(ctx context.Context, store StateStore, pgNode *PostgresNode) error {
	var status NodeStatus
	pgState, err := pgNode.FetchState()
	if err != nil {
		log.Printf("Failed to fetch Postgres node state: %v", err)
		errStr := err.Error()
		status.Error = &errStr
	} else {
		status.NodeTime = pgState.NodeTime
		status.IsPrimary = pgState.IsPrimary
		for _, replica := range pgState.PgStatReplicas {
			status.Replicas = append(status.Replicas, NodeReplicas{
				Hostname:  replica.ClientHostname,
				State:     replica.State,
				WriteLsn:  replica.WriteLsn,
				WriteLag:  replica.WriteLag,
				SyncState: replica.SyncState,
				ReplyTime: replica.ReplyTime,
			})
		}
		if pgState.PgStatWalReceiver != nil {
			status.ReplicationStatus = &NodeReplicationStatus{
				PrimaryHost: pgState.PgStatWalReceiver.SenderHost,
				Status:      pgState.PgStatWalReceiver.Status,
				WrittenLsn:  pgState.PgStatWalReceiver.WrittenLsn,
			}
		}
	}

	wCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
	err = store.WriteCurrentNodeStatus(wCtx, &status)
	cancel()
	if err != nil {
		return fmt.Errorf("failed to write node state to store: %w", err)
	}

	return nil
}

func performNodeTasks(ctx context.Context, store StateStore, conf config, pgNode *PostgresNode) error {
	spec, err := store.FetchClusterSpec(ctx)
	if err != nil {
		return fmt.Errorf("Failed to fetch node spec: %w", err)
	}

	log.Printf("Cluster spec for %s: %+v", conf.nodeName, spec)

	if spec.PrimaryName == conf.nodeName {
		if err := pgNode.ConfigureAsPrimary(ctx); err != nil {
			return fmt.Errorf("Failed to configure as primary: %w", err)
		}
	} else if slices.Contains(spec.ReplicaNames, conf.nodeName) {
		if err := pgNode.ConfigureAsReplica(ctx, spec.PrimaryName, conf.postgresPort, conf.postgresUser); err != nil {
			return fmt.Errorf("Failed to configure as replica: %w", err)
		}
	} else {
		return fmt.Errorf("Node %s is not a primary or replica in the cluster spec", conf.nodeName)
	}

	if err := ensurePgBouncerRunning(); err != nil {
		return fmt.Errorf("Failed to ensure PgBouncer is running: %w", err)
	}

	return nil
}
