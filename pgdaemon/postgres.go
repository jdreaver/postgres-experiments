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
	Error             *string                 `json:"error,omitempty"`
	NodeTime          *string                 `json:"node_time,omitempty"`
	IsPrimary         *bool                   `json:"is_primary,omitempty"`
	PgStatReplicas    []PostgresPgStatReplica `json:"pg_stat_replicas,omitempty"`
	PgStatWalReceiver *PgStatWalReceiver      `json:"pg_stat_wal_receiver,omitempty"`
}

type PostgresPgStatReplica struct {
	ClientHostname string  `json:"client_hostname"`
	ClientAddr     string  `json:"client_addr"`
	ClientPort     string  `json:"client_port"`
	State          string  `json:"state"`
	SentLsn        string  `json:"sent_lsn"`
	WriteLsn       string  `json:"write_lsn"`
	FlushLsn       string  `json:"flush_lsn"`
	ReplayLsn      string  `json:"replay_lsn"`
	WriteLag       *string `json:"write_lag"`
	FlushLag       *string `json:"flush_lag"`
	ReplayLag      *string `json:"replay_lag"`
	SyncState      string  `json:"sync_state"`
	ReplyTime      string  `json:"reply_time"`
}

type PgStatWalReceiver struct {
	SenderHost      string `json:"sender_host"`
	SenderPort      string `json:"sender_port"`
	Status          string `json:"status"`
	ReceiveStartLsn string `json:"receive_start_lsn"`
	WrittenLsn      string `json:"written_lsn"`
	FlushedLsn      string `json:"flushed_lsn"`
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

type HealthResponse struct {
	PostgresOK   bool   `json:"postgres_ok"`
	PostgresErr  string `json:"postgres_error,omitempty"`
	PgBouncerOK  bool   `json:"pgbouncer_ok"`
	PgBouncerErr string `json:"pgbouncer_error,omitempty"`
}

func checkDB(host string, port int, user string, connTimeout time.Duration) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), connTimeout)
	defer cancel()

	conn, err := connectPostgres(ctx, host, port, user)
	if err != nil {
		return false, fmt.Errorf("failed to connect to Postgres: %w", err)
	}
	defer conn.Close(ctx)

	var n int
	err = conn.QueryRow(ctx, "SELECT 1").Scan(&n)
	if err != nil {
		return false, fmt.Errorf("query error: %w", err)
	}
	if n != 1 {
		return false, fmt.Errorf("unexpected result from SELECT 1")
	}

	return true, nil
}

const pgDataDir = "/var/lib/postgres/data"

func configureAsReplica(ctx context.Context, primaryHost string, primaryPort int, user string) error {
	_, err := os.Stat(pgDataDir)
	if errors.Is(err, os.ErrNotExist) {
		log.Printf("Initializing replica for primary %s database in %s", primaryHost, pgDataDir)

		// TODO: Ensure postgres is not running

		cmd := exec.Command("pg_basebackup", "-h", primaryHost, "-p", fmt.Sprintf("%d", primaryPort), "-U", user, "-D", pgDataDir, "-R", "-P")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to initialize replica database: %w", err)
		}
	}

	// TODO: Check if pg_is_in_recovery() is false. If so, we need
	// to point to new primary and become a replica.

	return nil
}

func configureAsPrimary() error {
	_, err := os.Stat(pgDataDir)
	if errors.Is(err, os.ErrNotExist) {
		log.Printf("Initializing primary database in %s", pgDataDir)

		// TODO: Ensure postgres is not running

		cmd := exec.Command("initdb", "--pgdata", pgDataDir)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to initialize primary database: %w", err)
		}
	}

	// TODO: Check if pg_is_in_recovery() is true, and do a
	// pg_promote() (probably requires careful coordination)

	return nil
}

func ensurePostgresRunning() error {
	// TODO: We should be able to surmise whether or not we need to
	// do this based on the state we fetch about the node. If we
	// can't connect, we should check if postgres is running, and
	// cache that result.

	cmd := exec.Command("systemctl", "is-active", "--quiet", "postgresql.service")
	if err := cmd.Run(); err != nil {
		log.Println("Postgres might not not running, attempting to start it")
		startCmd := exec.Command("sudo", "systemctl", "start", "postgresql.service")
		startCmd.Stdout = os.Stdout
		startCmd.Stderr = os.Stderr
		if err := startCmd.Run(); err != nil {
			return fmt.Errorf("failed to start Postgres: %w", err)
		}
	}

	return nil
}
