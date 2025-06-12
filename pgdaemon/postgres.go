package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

func connectPostgres(ctx context.Context, host string, port int, user string) (*pgx.Conn, error) {
	// N.B. default_query_exec_mode=exec because the default uses
	// statement caching, which doesn't work with pgbouncer.
	dsn := fmt.Sprintf("postgres://%s@%s:%d/?sslmode=disable&default_query_exec_mode=exec", user, host, port)
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("pgx connect error: %w", err)
	}
	return conn, nil
}

type PostgresNodeState struct {
	NodeTime          *string
	IsPrimary         *bool
	PgStatReplicas    []PostgresPgStatReplica
	PgStatWalReceiver *PgStatWalReceiver
}

type PostgresPgStatReplica struct {
	ClientHostname string
	ClientAddr     string
	ClientPort     string
	State          string
	SentLsn        string
	WriteLsn       string
	FlushLsn       string
	ReplayLsn      string
	WriteLag       *string
	FlushLag       *string
	ReplayLag      *string
	SyncState      string
	ReplyTime      string
}

type PgStatWalReceiver struct {
	SenderHost      string
	SenderPort      string
	Status          string
	ReceiveStartLsn *string
	WrittenLsn      *string
	FlushedLsn      *string
}

func fetchPostgresNodeState(host string, port int, user string, connTimeout time.Duration) (*PostgresNodeState, error) {
	ctx, cancel := context.WithTimeout(context.Background(), connTimeout)
	defer cancel()

	conn, err := connectPostgres(ctx, host, port, user)
	if err != nil {
		return nil, fmt.Errorf("connect to Postgres: %w", err)
	}
	defer conn.Close(ctx)

	var state PostgresNodeState
	if err := conn.QueryRow(ctx, "SELECT now(), NOT pg_is_in_recovery()").Scan(&state.NodeTime, &state.IsPrimary); err != nil {
		return nil, fmt.Errorf("check pg_is_in_recovery: %w", err)
	}

	if *state.IsPrimary {
		rows, err := conn.Query(ctx, `
			SELECT client_hostname, client_addr, client_port, state, sent_lsn,
			       write_lsn, flush_lsn, replay_lsn, write_lag, flush_lag,
			       replay_lag, sync_state, reply_time
			FROM pg_stat_replication`)
		if err != nil {
			return nil, fmt.Errorf("query pg_stat_replication: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var r PostgresPgStatReplica
			if err := rows.Scan(
				&r.ClientHostname, &r.ClientAddr, &r.ClientPort, &r.State,
				&r.SentLsn, &r.WriteLsn, &r.FlushLsn, &r.ReplayLsn,
				&r.WriteLag, &r.FlushLag, &r.ReplayLag,
				&r.SyncState, &r.ReplyTime,
			); err != nil {
				return nil, fmt.Errorf("scan pg_stat_replication row: %w", err)
			}
			state.PgStatReplicas = append(state.PgStatReplicas, r)
		}

		return &state, nil
	}

	var receiver PgStatWalReceiver
	if err := conn.QueryRow(ctx, `
		SELECT sender_host, sender_port, status,
		       receive_start_lsn, written_lsn, flushed_lsn
		FROM pg_stat_wal_receiver`,
	).Scan(
		&receiver.SenderHost, &receiver.SenderPort, &receiver.Status,
		&receiver.ReceiveStartLsn, &receiver.WrittenLsn, &receiver.FlushedLsn,
	); err != nil {
		return nil, fmt.Errorf("query pg_stat_wal_receiver: %w", err)
	}
	state.PgStatWalReceiver = &receiver
	return &state, nil
}

func checkIsPrimary(host string, port int, user string, connTimeout time.Duration) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), connTimeout)
	defer cancel()

	conn, err := connectPostgres(ctx, host, port, user)
	if err != nil {
		return false, fmt.Errorf("failed to connect to Postgres: %w", err)
	}
	defer conn.Close(ctx)

	var isPrimary bool
	if err := conn.QueryRow(ctx, "SELECT NOT pg_is_in_recovery()").Scan(&isPrimary); err != nil {
		return false, fmt.Errorf("check pg_is_in_recovery: %w", err)
	}

	return isPrimary, nil
}

const pgDataDir = "/var/lib/postgres/data"
const pgVersionFile = pgDataDir + "/PG_VERSION"

func configureAsReplica(primaryHost string, primaryPort int, user string) error {
	_, err := os.Stat(pgVersionFile)
	if errors.Is(err, os.ErrNotExist) {
		log.Printf("Initializing replica for primary %s database in %s", primaryHost, pgDataDir)

		// TODO: Ensure postgres is not running

		cmd := exec.Command("pg_basebackup", "-h", primaryHost, "-p", fmt.Sprintf("%d", primaryPort), "-U", user, "-D", pgDataDir, "-R", "-P")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to initialize replica database: %w", err)
		}

		if err := commonPostgresConfig(); err != nil {
			return fmt.Errorf("failed to configure replica database: %w", err)
		}
	} else {
		// Database already exists, check if we need to reconfigure for new primary
		if err := updateReplicaConfiguration(primaryHost, primaryPort); err != nil {
			log.Printf("Failed to update replica configuration: %v", err)
			// For now, just log the error and continue
		}
	}

	return nil
}

func updateReplicaConfiguration(primaryHost string, primaryPort int) error {
	// TODO: Use `ALTER SYSTEM SET` to change primary_conninfo
	// instead of editing this file.

	// Update postgresql.auto.conf to point to new primary
	autoConfPath := pgDataDir + "/postgresql.auto.conf"

	primaryConnInfo := fmt.Sprintf("host=%s port=%d user=postgres", primaryHost, primaryPort)

	// Read existing auto.conf
	content := ""
	if data, err := os.ReadFile(autoConfPath); err == nil {
		content = string(data)
	}

	// Update or add primary_conninfo
	lines := strings.Split(content, "\n")
	found := false
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "primary_conninfo") {
			lines[i] = fmt.Sprintf("primary_conninfo = '%s'", primaryConnInfo)
			found = true
			break
		}
	}

	if !found {
		lines = append(lines, fmt.Sprintf("primary_conninfo = '%s'", primaryConnInfo))
	}

	// Write back to file
	newContent := strings.Join(lines, "\n")
	if err := os.WriteFile(autoConfPath, []byte(newContent), 0644); err != nil {
		return fmt.Errorf("failed to update postgresql.auto.conf: %w", err)
	}

	log.Printf("Updated replica configuration to connect to primary %s:%d", primaryHost, primaryPort)
	return nil
}

func configureAsPrimary() error {
	_, err := os.Stat(pgVersionFile)
	if errors.Is(err, os.ErrNotExist) {
		log.Printf("Initializing primary database in %s", pgDataDir)

		// TODO: Ensure postgres is not running

		cmd := exec.Command("initdb", "--pgdata", pgDataDir)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to initialize primary database: %w", err)
		}

		if err := commonPostgresConfig(); err != nil {
			return fmt.Errorf("failed to configure primary database: %w", err)
		}
	}

	// TODO: Check if pg_is_in_recovery() is true, and do a
	// pg_promote() (probably requires careful coordination)

	return nil
}

// TODO: Specify this stuff in a config file. Or, move database
// initialization entirely out of pgdaemon somehow and assume PGDATA
// exists?
func commonPostgresConfig() error {
	err := appendToFile(pgDataDir+"/postgresql.conf", `
# Bind to all interfaces
listen_addresses = '*'

# More logging
log_connections = on
log_hostname = on

# More settings
synchronous_commit = off
work_mem = 64MB
maintenance_work_mem = 2GB

# Support replication
wal_level = logical
`)

	if err != nil {
		return fmt.Errorf("Failed to append to postgresql.conf: %w", err)
	}

	err = appendToFile(pgDataDir+"/pg_hba.conf", `
# Allow connections from all hosts, without password
host    all             all             0.0.0.0/0            trust

# Allow replication from all hosts
host    replication     all             0.0.0.0/0            trust
`)
	if err != nil {
		return fmt.Errorf("Failed to append to pg_hba.conf: %w", err)
	}

	return nil
}

func appendToFile(path string, content string) error {
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %w", path, err)
	}
	defer file.Close()

	if _, err := file.WriteString(content); err != nil {
		return fmt.Errorf("failed to write to file %s: %w", path, err)
	}

	return nil
}

func ensurePostgresRunning() error {
	return ensureSystemdUnitRunning("postgresql.service")
}

func ensurePgBouncerRunning() error {
	return ensureSystemdUnitRunning("pgbouncer.service")
}

func ensureSystemdUnitRunning(name string) error {
	// TODO: We should be able to surmise whether or not we need to
	// do this based on the state we fetch about the node. If we
	// can't connect, we should check if postgres/pgbouncer is
	// running, and cache that result.
	cmd := exec.Command("systemctl", "is-active", "--quiet", name)
	if err := cmd.Run(); err != nil {
		log.Printf("%s might not not running, attempting to start it", name)
		startCmd := exec.Command("sudo", "systemctl", "start", name)
		startCmd.Stdout = os.Stdout
		startCmd.Stderr = os.Stderr
		if err := startCmd.Run(); err != nil {
			return fmt.Errorf("failed to start %s: %w", name, err)
		}
	}

	return nil
}

// stopPostgres gracefully stops the PostgreSQL service
func stopPostgres() error {
	log.Printf("Stopping PostgreSQL service")
	cmd := exec.Command("sudo", "systemctl", "stop", "postgresql.service")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to stop PostgreSQL: %w", err)
	}
	return nil
}

// promoteReplica promotes a replica to become the new primary
func promoteReplica(host string, port int, user string) error {
	log.Printf("Promoting replica at %s:%d to primary", host, port)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := connectPostgres(ctx, host, port, user)
	if err != nil {
		return fmt.Errorf("failed to connect to replica for promotion: %w", err)
	}
	defer conn.Close(ctx)

	// Check if this is actually a replica
	var isInRecovery bool
	if err := conn.QueryRow(ctx, "SELECT pg_is_in_recovery()").Scan(&isInRecovery); err != nil {
		return fmt.Errorf("failed to check recovery status: %w", err)
	}

	if !isInRecovery {
		return fmt.Errorf("node is not in recovery mode - cannot promote")
	}

	// Promote the replica
	if _, err := conn.Exec(ctx, "SELECT pg_promote()"); err != nil {
		return fmt.Errorf("failed to promote replica: %w", err)
	}

	log.Printf("Successfully promoted replica at %s:%d to primary", host, port)
	return nil
}

// parseLSN parses a PostgreSQL LSN string and returns a comparable value
func parseLSN(lsn string) (uint64, error) {
	if lsn == "" {
		return 0, nil
	}

	var high, low uint32
	if _, err := fmt.Sscanf(lsn, "%X/%X", &high, &low); err != nil {
		return 0, fmt.Errorf("failed to parse LSN %s: %w", lsn, err)
	}

	return (uint64(high) << 32) | uint64(low), nil
}

// findBestReplica finds the replica with the highest written LSN
func findBestReplica(replicas map[string]*NodeObservedState, excludePrimary string) (string, error) {
	var bestNode string
	var bestLSN uint64

	for nodeName, state := range replicas {
		if nodeName == excludePrimary {
			continue
		}

		// Skip nodes with errors
		if state.Error != nil {
			log.Printf("Skipping node %s due to error: %s", nodeName, *state.Error)
			continue
		}

		// Skip nodes that are primaries
		if state.IsPrimary != nil && *state.IsPrimary {
			log.Printf("Skipping node %s - it's a primary", nodeName)
			continue
		}

		// Check if this node has replication status
		if state.PgStatWalReceiver == nil || state.PgStatWalReceiver.WrittenLsn == nil {
			log.Printf("Skipping node %s - no replication status", nodeName)
			continue
		}

		lsn, err := parseLSN(*state.PgStatWalReceiver.WrittenLsn)
		if err != nil {
			log.Printf("Skipping node %s - failed to parse LSN: %v", nodeName, err)
			continue
		}

		if lsn > bestLSN {
			bestLSN = lsn
			bestNode = nodeName
		}
	}

	if bestNode == "" {
		return "", fmt.Errorf("no suitable replica found for promotion")
	}

	log.Printf("Selected %s as best replica (LSN: %s)", bestNode, *replicas[bestNode].PgStatWalReceiver.WrittenLsn)
	return bestNode, nil
}
