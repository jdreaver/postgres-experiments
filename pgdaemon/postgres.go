package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
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
	NodeTime          string
	IsPrimary         bool
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

	if state.IsPrimary {
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

func configureAsReplica(ctx context.Context, host string, port int, primaryHost string, primaryPort int, user string) error {
	if _, err := os.Stat(pgVersionFile); errors.Is(err, os.ErrNotExist) {
		log.Printf("Initializing replica for primary %s database in %s", primaryHost, pgDataDir)

		// TODO: Ensure postgres is not running

		cmd := exec.Command("pg_basebackup", "-h", primaryHost, "-p", fmt.Sprintf("%d", primaryPort), "-U", user, "-D", pgDataDir, "-R", "-P")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to initialize replica database: %w", err)
		}

		// N.B. pg_basebackup copies all .conf files as well
	}

	// Ensure standby.signal exists
	standbySignalPath := pgDataDir + "/standby.signal"
	if _, err := os.Stat(standbySignalPath); errors.Is(err, os.ErrNotExist) {
		log.Printf("Creating standby.signal in %s", pgDataDir)
		if err := os.WriteFile(standbySignalPath, []byte{}, 0644); err != nil {
			return fmt.Errorf("failed to create standby.signal: %w", err)
		}
	}

	if err := ensurePostgresRunning(); err != nil {
		return fmt.Errorf("Failed to ensure Postgres is running: %w", err)
	}

	// Check if pg_is_in_recovery() is false. If so, we need
	// to point to new primary and become a replica.
	conn, err := connectPostgres(ctx, host, port, user)
	if err != nil {
		return fmt.Errorf("failed to connect to Postgres: %w", err)
	}
	defer conn.Close(ctx)

	// Fetch conninfo to see if we need to change it
	var currentConninfo string
	if err := conn.QueryRow(ctx, "SHOW primary_conninfo").Scan(&currentConninfo); err != nil {
		return fmt.Errorf("failed to get current primary_conninfo: %w", err)
	}

	expectedConninfo := fmt.Sprintf("host=%s port=%d user=%s", primaryHost, primaryPort, user)

	if currentConninfo != expectedConninfo {
		log.Printf("Primary connection info is %s, changing to %s", currentConninfo, expectedConninfo)

		query := fmt.Sprintf("ALTER SYSTEM SET primary_conninfo = '%s'", expectedConninfo)
		if _, err := conn.Exec(ctx, query); err != nil {
			return fmt.Errorf("failed to set primary_conninfo: %w", err)
		}
		if _, err := conn.Exec(ctx, "SELECT pg_reload_conf()"); err != nil {
			return fmt.Errorf("failed to reload Postgres configuration: %w", err)
		}

		// Kill walreceiver to force a reconnect TODO: Make this more
		// robust? What if primary_conninfo was properly set but we
		// failed before this line. We might never restart walreceiver.
		if _, err := conn.Exec(ctx, "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE application_name = 'walreceiver'"); err != nil {
			return fmt.Errorf("failed to terminate walreceiver: %w", err)
		}
	}

	var isInRecovery bool
	if err := conn.QueryRow(ctx, "SELECT pg_is_in_recovery()").Scan(&isInRecovery); err != nil {
		return fmt.Errorf("failed to check pg_is_in_recovery: %w", err)
	}
	if !isInRecovery {
		log.Printf("Postgres is not in recovery mode. Must be the old primary. Need to stop")
		if err := stopPostgres(); err != nil {
			return fmt.Errorf("failed to stop Postgres: %w", err)
		}

		// Run pg_rewind. TODO: We don't necessarily need to do
		// this every time, but since we must stop the primary
		// anyway we can be safe.
		cmd := exec.Command("pg_rewind", "--target-pgdata", pgDataDir, "--source-server=host="+primaryHost+" port="+fmt.Sprintf("%d", primaryPort)+" user="+user)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to run pg_rewind: %w", err)
		}
		log.Printf("pg_rewind completed successfully, Postgres should now be a replica of %s", primaryHost)

		if err := ensurePostgresRunning(); err != nil {
			return fmt.Errorf("Failed to ensure Postgres is running after pg_rewind: %w", err)
		}
		log.Printf("Postgres restarted successfully after pg_rewind, now a replica of %s", primaryHost)
	}

	return nil
}

func configureAsPrimary(ctx context.Context, host string, port int, user string) error {
	if _, err := os.Stat(pgVersionFile); errors.Is(err, os.ErrNotExist) {
		log.Printf("Initializing primary database in %s", pgDataDir)

		// TODO: Ensure postgres is not running

		cmd := exec.Command("initdb", "--pgdata", pgDataDir)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to initialize primary database: %w", err)
		}

		if err := writePostgresConfFiles(); err != nil {
			return fmt.Errorf("failed to configure primary database: %w", err)
		}
	}

	if err := ensurePostgresRunning(); err != nil {
		return fmt.Errorf("Failed to ensure Postgres is running: %w", err)
	}

	conn, err := connectPostgres(ctx, host, port, user)
	if err != nil {
		return fmt.Errorf("failed to connect to Postgres: %w", err)
	}
	defer conn.Close(ctx)

	var isInRecovery bool
	if err := conn.QueryRow(ctx, "SELECT pg_is_in_recovery()").Scan(&isInRecovery); err != nil {
		return fmt.Errorf("failed to check pg_is_in_recovery: %w", err)
	}
	if !isInRecovery {
		log.Printf("Postgres is already not in recovery mode, no need to configure as primary")
		return nil
	}

	// Run pg_promote to become primary
	if _, err := conn.Exec(ctx, "SELECT pg_promote(wait => true)"); err != nil {
		return fmt.Errorf("failed to promote Postgres to primary: %w", err)
	}
	log.Printf("Postgres promoted to primary successfully")

	return nil
}

// TODO: Specify some of this stuff in a config file. Or, move database
// initialization entirely out of pgdaemon somehow and assume PGDATA
// exists?
const pgdaemonConfContent = `
# Bind to all interfaces
listen_addresses = '*'

# More logging
log_connections = on
log_hostname = on

# More settings
synchronous_commit = off
work_mem = 64MB
maintenance_work_mem = 2GB

# Store more WAL so replicas can catch up and we can pg_rewind
wal_keep_size = 2GB

# Support pg_rewind
wal_log_hints = on

# Support logical replication
wal_level = logical
`

const hbaConfContent = `
# Allow connections from all hosts, without password
host    all             all             0.0.0.0/0            trust

# Allow replication from all hosts
host    replication     all             0.0.0.0/0            trust
`

func writePostgresConfFiles() error {
	if err := appendToFile(pgDataDir+"/postgresql.conf", "include_dir 'postgresql.conf.d'"); err != nil {
		return fmt.Errorf("Failed to append to postgresql.conf: %w", err)
	}

	if err := os.MkdirAll(pgDataDir+"/postgresql.conf.d", 0755); err != nil {
		return fmt.Errorf("Failed to create postgresql.conf.d directory: %w", err)
	}

	if err := os.WriteFile(pgDataDir+"/postgresql.conf.d/pgdaemon.conf", []byte(pgdaemonConfContent), 0644); err != nil {
		return fmt.Errorf("Failed to write pgdaemon.conf: %w", err)
	}

	if err := appendToFile(pgDataDir+"/pg_hba.conf", hbaConfContent); err != nil {
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

const postgresSystemdUnit = "postgresql.service"
const pgBouncerSystemdUnit = "pgbouncer.service"

func ensurePostgresRunning() error {
	return ensureSystemdUnitRunning(postgresSystemdUnit)
}

func stopPostgres() error {
	return runSystemctl("stop", postgresSystemdUnit)
}

func ensurePgBouncerRunning() error {
	return ensureSystemdUnitRunning(pgBouncerSystemdUnit)
}

func ensureSystemdUnitRunning(name string) error {
	// TODO: We should be able to surmise whether or not we need to
	// do this based on the state we fetch about the node. If we
	// can't connect, we should check if postgres/pgbouncer is
	// running, and cache that result.
	cmd := exec.Command("systemctl", "is-active", "--quiet", name)
	if err := cmd.Run(); err != nil {
		log.Printf("%s might not not running, attempting to start it", name)
		return runSystemctl("start", name)
	}

	return nil
}

func runSystemctl(command string, name string) error {
	cmd := exec.Command("sudo", "systemctl", command, name)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to %s %s: %w", command, name, err)
	}
	log.Printf("Ran systemctl %s %s successfully", command, name)
	return nil
}
