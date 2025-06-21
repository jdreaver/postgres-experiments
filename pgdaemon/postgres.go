package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresNode struct {
	pool          *pgxpool.Pool
	pgBouncerPool *pgxpool.Pool
}

func NewPostgresNode(host string, port int, user string, pgBouncerHost string, pgBouncerPort int) (*PostgresNode, error) {
	if host == "" || port <= 0 || user == "" {
		return nil, fmt.Errorf("invalid Postgres connection parameters: host=%s, port=%d, user=%s", host, port, user)
	}

	pool, err := connectPostgresPool(host, port, user)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Postgres: %w", err)
	}

	pgBouncerPool, err := connectPostgresPool(pgBouncerHost, pgBouncerPort, user)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Postgres: %w", err)
	}

	log.Printf("Connected to Postgres at %s:%d as user %s", host, port, user)
	return &PostgresNode{
		pool:          pool,
		pgBouncerPool: pgBouncerPool,
	}, nil
}

const maxPoolSize = 3
const poolIdleTime = 10 * time.Second
const localQueryTimeout = 200 * time.Millisecond

func connectPostgresPool(host string, port int, user string) (*pgxpool.Pool, error) {
	// N.B. default_query_exec_mode=exec because the default uses
	// statement caching, which doesn't work with pgbouncer.
	connStr := fmt.Sprintf("host=%s port=%d user=%s sslmode=disable default_query_exec_mode=exec pool_max_conns=%d pool_max_conn_idle_time=%s", host, port, user, maxPoolSize, poolIdleTime)
	config, err := pgxpool.ParseConfig(connStr)
	if err != nil {
		return nil, fmt.Errorf("pgx parse config error: %w", err)
	}

	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		return nil, fmt.Errorf("pgxpool connect error: %w", err)
	}
	return pool, nil
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
	SentLsn        *string
	WriteLsn       *string
	FlushLsn       *string
	ReplayLsn      *string
	WriteLag       *string
	FlushLag       *string
	ReplayLag      *string
	SyncState      *string
	ReplyTime      *string
}

type PgStatWalReceiver struct {
	SenderHost      string
	SenderPort      string
	Status          string
	ReceiveStartLsn *string
	WrittenLsn      *string
	FlushedLsn      *string
}

func (p *PostgresNode) FetchState() (*PostgresNodeState, error) {
	ctx, cancel := context.WithTimeout(context.Background(), localQueryTimeout)
	defer cancel()

	var state PostgresNodeState
	if err := p.pool.QueryRow(ctx, "SELECT now(), NOT pg_is_in_recovery()").Scan(&state.NodeTime, &state.IsPrimary); err != nil {
		return nil, fmt.Errorf("check pg_is_in_recovery: %w", err)
	}

	if state.IsPrimary {
		rows, err := p.pool.Query(ctx, `
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
	if err := p.pool.QueryRow(ctx, `
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

func CheckIsPrimary(pool *pgxpool.Pool) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), localQueryTimeout)
	defer cancel()

	var isPrimary bool
	if err := pool.QueryRow(ctx, "SELECT NOT pg_is_in_recovery()").Scan(&isPrimary); err != nil {
		return false, fmt.Errorf("check pg_is_in_recovery: %w", err)
	}

	return isPrimary, nil
}

const pgDataDir = "/var/lib/postgres/data"
const pgVersionFile = pgDataDir + "/PG_VERSION"

func (p *PostgresNode) ConfigureAsReplica(ctx context.Context, primaryHost string, primaryPort int, user string) error {
	if _, err := os.Stat(pgVersionFile); errors.Is(err, os.ErrNotExist) {
		log.Printf("Initializing replica for primary %s database in %s", primaryHost, pgDataDir)

		cmd := exec.Command("pg_basebackup", "-h", primaryHost, "-p", fmt.Sprintf("%d", primaryPort), "-U", user, "-D", pgDataDir, "--progress")
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

	// Check conninfo to see if we need to change it
	conninfoPath := pgDataDir + "/postgresql.conf.d/primary_conninfo.conf"
	currentConninfo, err := os.ReadFile(conninfoPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed to read primary_conninfo.conf: %w", err)
	}

	expectedConninfo := fmt.Appendf(nil, "primary_conninfo = 'host=%s port=%d user=%s'", primaryHost, primaryPort, user)
	if string(currentConninfo) != string(expectedConninfo) {
		log.Printf("Primary connection info is %s, changing to %s", currentConninfo, expectedConninfo)

		if err := os.WriteFile(conninfoPath, expectedConninfo, 0644); err != nil {
			return fmt.Errorf("failed to write primary_conninfo.conf: %w", err)
		}

		// Call systemctl reload postgresql.service if running
		if err := systemctlCommandIfRunning("reload", postgresSystemdUnit); err != nil {
			return fmt.Errorf("failed to reload Postgres service: %w", err)
		}

		// TODO: Kill walreceiver processes to force a reconnect.
		// TODO: Make this more robust? What if primary_conninfo was
		// properly set but we failed before this line. We might
		// never restart walreceiver.
		// log.Printf("Killing walreceiver processes to force reconnect to primary %s", primaryHost)
		// cmd := exec.Command("pkill", "-f", "walreceiver")
		// cmd.Stdout = os.Stdout
		// cmd.Stderr = os.Stderr
		// if err := cmd.Run(); err != nil {
		// 	// TODO: What if the error is that we couldn't kill the walreciever process?
		// 	log.Printf("Failed to kill walreceiver processes, might not have been running: %v", err)
		// }
	}

	if err := ensurePostgresRunning(); err != nil {
		return fmt.Errorf("Failed to ensure Postgres is running: %w", err)
	}

	// Check if pg_is_in_recovery() is false. If so, we need
	// to point to new primary and become a replica.
	var isInRecovery bool
	if err := p.pool.QueryRow(ctx, "SELECT pg_is_in_recovery()").Scan(&isInRecovery); err != nil {
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

func (p *PostgresNode) ConfigureAsPrimary(ctx context.Context) error {
	if _, err := os.Stat(pgVersionFile); errors.Is(err, os.ErrNotExist) {
		log.Printf("Initializing primary database in %s", pgDataDir)

		cmd := exec.Command("pg_ctl", "initdb", "--pgdata", pgDataDir)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to initialize primary database: %w", err)
		}

		if err := writePostgresConfFiles(); err != nil {
			return fmt.Errorf("failed to configure primary database: %w", err)
		}
	}

	// Ensure primary_conninfo.conf is nuked
	conninfoPath := pgDataDir + "/postgresql.conf.d/primary_conninfo.conf"
	if err := os.Remove(conninfoPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed to remove primary_conninfo.conf: %w", err)
	}

	if err := ensurePostgresRunning(); err != nil {
		return fmt.Errorf("Failed to ensure Postgres is running: %w", err)
	}

	var isInRecovery bool
	if err := p.pool.QueryRow(ctx, "SELECT pg_is_in_recovery()").Scan(&isInRecovery); err != nil {
		return fmt.Errorf("failed to check pg_is_in_recovery: %w", err)
	}
	if !isInRecovery {
		return nil
	}

	// Run pg_promote to become primary
	if _, err := p.pool.Exec(ctx, "SELECT pg_promote(wait => true)"); err != nil {
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
	return systemctlCommandIfNotRunning("start", postgresSystemdUnit)
}

func stopPostgres() error {
	return runSystemctl("stop", postgresSystemdUnit)
}

func ensurePgBouncerRunning() error {
	return systemctlCommandIfNotRunning("start", pgBouncerSystemdUnit)
}

func systemctlCommandIfRunning(command string, name string) error {
	cmd := exec.Command("systemctl", "is-active", "--quiet", name)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return nil
	}
	return runSystemctl(command, name)
}

func systemctlCommandIfNotRunning(command string, name string) error {
	cmd := exec.Command("systemctl", "is-active", "--quiet", name)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return runSystemctl(command, name)
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
