package main

import (
	"context"
	"fmt"
	"log"
	"time"
)

type NodeObservedState struct {
	Error             *string                `json:"error,omitempty"`
	NodeTime          *string                `json:"node_time,omitempty"`
	IsPrimary         *bool                  `json:"is_primary,omitempty"`
	Replicas          []NodeReplicas         `json:"replicas,omitempty"`
	PgStatWalReceiver *NodeReplicationStatus `json:"replication_status,omitempty"`
}

type NodeReplicas struct {
	Hostname  string  `json:"hostname"`
	State     string  `json:"state"`
	WriteLsn  string  `json:"write_lsn"`
	WriteLag  *string `json:"write_lag"`
	SyncState string  `json:"sync_state"`
	ReplyTime string  `json:"reply_time"`
}

type NodeReplicationStatus struct {
	PrimaryHost string  `json:"primary_host"`
	Status      string  `json:"status"`
	WrittenLsn  *string `json:"written_lsn"`
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
			if err := storeObservedState(ctx, store, conf); err != nil {
				log.Printf("Failed to store observed state: %v", err)
			}

			if err := performNodeTasks(ctx, store, conf); err != nil {
				log.Printf("Failed to perform node tasks: %v", err)
			}
		}
	}
}

func storeObservedState(ctx context.Context, store StateStore, conf config) error {
	var state NodeObservedState
	pgState, err := fetchPostgresNodeState(conf.postgresHost, conf.postgresPort, conf.postgresUser, 500*time.Millisecond)
	if err != nil {
		log.Printf("Failed to fetch Postgres node state: %v", err)
		errStr := err.Error()
		state.Error = &errStr
	} else {
		state.NodeTime = pgState.NodeTime
		state.IsPrimary = pgState.IsPrimary
		for _, replica := range pgState.PgStatReplicas {
			state.Replicas = append(state.Replicas, NodeReplicas{
				Hostname:  replica.ClientHostname,
				State:     replica.State,
				WriteLsn:  replica.WriteLsn,
				WriteLag:  replica.WriteLag,
				SyncState: replica.SyncState,
				ReplyTime: replica.ReplyTime,
			})
		}
		if pgState.PgStatWalReceiver != nil {
			state.PgStatWalReceiver = &NodeReplicationStatus{
				PrimaryHost: pgState.PgStatWalReceiver.SenderHost,
				Status:      pgState.PgStatWalReceiver.Status,
				WrittenLsn:  pgState.PgStatWalReceiver.WrittenLsn,
			}
		}
	}

	wCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
	err = store.WriteCurrentNodeObservedState(wCtx, &state)
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

	// Check current PostgreSQL state
	isPrimary, err := checkIsPrimary(conf.postgresHost, conf.postgresPort, conf.postgresUser, 500*time.Millisecond)
	if err != nil {
		log.Printf("Failed to check if node is primary (postgres might be down): %v", err)
		isPrimary = false
	}

	if desiredState.PrimaryName == conf.nodeName {
		// This node should be the primary
		if !isPrimary {
			// We need to promote this replica to primary
			log.Printf("Node %s needs to be promoted to primary", conf.nodeName)
			err = promoteReplica(conf.postgresHost, conf.postgresPort, conf.postgresUser)
			if err != nil {
				return fmt.Errorf("Failed to promote replica to primary: %w", err)
			}
		} else {
			// Already primary, ensure configuration is correct
			err = configureAsPrimary()
			if err != nil {
				return fmt.Errorf("Failed to configure as primary: %w", err)
			}
		}
	} else {
		// This node should be a replica
		if isPrimary {
			// We need to demote this primary and reconfigure as replica
			log.Printf("Node %s needs to be demoted from primary to replica", conf.nodeName)
			err = stopPostgres()
			if err != nil {
				return fmt.Errorf("Failed to stop postgres for demotion: %w", err)
			}
		}

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
