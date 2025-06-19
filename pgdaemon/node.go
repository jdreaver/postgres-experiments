package main

import (
	"context"
	"fmt"
	"log"
	"slices"
	"time"

	"github.com/google/uuid"
)

// nodeReconcilerLoop runs the node reconciler, which fetches the spec
// and status of the current node and performs tasks to reconcile them.
func nodeReconcilerLoop(ctx context.Context, store StateStore, conf config, pgNode *PostgresNode, wakeupManager *WakeupManager) error {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	var wakeupChan <-chan struct{}
	if wakeupManager != nil {
		wakeupChan = wakeupManager.WakeupChannel()
	}

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("returning ctx.Done() error in node reconciler loop: %w", ctx.Err())
		case <-ticker.C:
			if err := performReconciliationCycle(ctx, store, conf, pgNode, wakeupManager); err != nil {
				log.Printf("Failed to perform reconciliation cycle: %v", err)
			}
		case <-wakeupChan:
			log.Printf("Wakeup received, performing immediate reconciliation")
			if err := performReconciliationCycle(ctx, store, conf, pgNode, wakeupManager); err != nil {
				log.Printf("Failed to perform reconciliation cycle: %v", err)
			}
		}
	}
}

// performReconciliationCycle performs one full reconciliation cycle
func performReconciliationCycle(ctx context.Context, store StateStore, conf config, pgNode *PostgresNode, wakeupManager *WakeupManager) error {
	if err := storeNodeStatus(ctx, store, conf.nodeName, pgNode); err != nil {
		log.Printf("Failed to store node status: %v", err)
	}

	if err := performNodeTasks(ctx, store, conf, pgNode, wakeupManager); err != nil {
		return fmt.Errorf("Failed to perform node tasks: %w", err)
	}

	return nil
}

func storeNodeStatus(ctx context.Context, store StateStore, nodeName string, pgNode *PostgresNode) error {
	var status NodeStatus
	status.Name = nodeName
	status.StatusUuid = uuid.New()

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

func performNodeTasks(ctx context.Context, store StateStore, conf config, pgNode *PostgresNode, wakeupManager *WakeupManager) error {
	state, err := store.FetchClusterState(ctx)
	if err != nil {
		return fmt.Errorf("Failed to fetch node spec: %w", err)
	}

	newStatus := ComputeNewClusterStatus(state)
	newStatus, statusChanged, err := WriteClusterStatusIfChanged(store, state.Status, newStatus, conf.nodeName)
	if err != nil {
		return fmt.Errorf("Failed to write cluster status: %w", err)
	}

	// Send wakeup packets if cluster status changed and wakeup is enabled
	if wakeupManager != nil && statusChanged {
		peerHostnames := extractPeerHostnames(state.Nodes, conf.nodeName)
		if len(peerHostnames) > 0 {
			wakeupManager.SendWakeupToNodes(peerHostnames)
		}
	}

	state.Status = newStatus

	if state.Status.IntendedPrimary == conf.nodeName {
		if err := pgNode.ConfigureAsPrimary(ctx); err != nil {
			return fmt.Errorf("Failed to configure as primary: %w", err)
		}
	} else if slices.Contains(state.Status.IntendedReplicas, conf.nodeName) {
		if err := pgNode.ConfigureAsReplica(ctx, state.Status.IntendedPrimary, conf.postgresPort, conf.postgresUser); err != nil {
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

// extractPeerHostnames extracts hostnames of all peer nodes in the cluster except the current node
// This function assumes node names are either hostnames or can be resolved as hostnames
func extractPeerHostnames(nodes []NodeStatus, currentNodeName string) []string {
	var peerHostnames []string
	for _, node := range nodes {
		if node.Name != currentNodeName && node.Name != "" {
			peerHostnames = append(peerHostnames, node.Name)
		}
	}
	return peerHostnames
}
