package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/concurrency"
)

type HealthResponse struct {
	PostgresOK   bool   `json:"postgres_ok"`
	PostgresErr  string `json:"postgres_error,omitempty"`
	PgBouncerOK  bool   `json:"pgbouncer_ok"`
	PgBouncerErr string `json:"pgbouncer_error,omitempty"`
}

func checkDB(host string, port int, user string, connTimeout time.Duration) (bool, string) {
	ctx, cancel := context.WithTimeout(context.Background(), connTimeout)
	defer cancel()

	// N.B. default_query_exec_mode=exec because the default uses
	// statement caching, which doesn't work with pgbouncer.
	dsn := fmt.Sprintf("postgres://%s@%s:%d/?sslmode=disable&default_query_exec_mode=exec", user, host, port)
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return false, fmt.Sprintf("connect error: %v", err)
	}
	defer conn.Close(ctx)

	var n int
	err = conn.QueryRow(ctx, "SELECT 1").Scan(&n)
	if err != nil {
		return false, fmt.Sprintf("query error: %v", err)
	}
	if n != 1 {
		return false, "unexpected result from SELECT 1"
	}

	return true, ""
}

func main() {
	etcdHost := flag.String("etcd-host", "127.0.0.1", "etcd host")
	etcdPort := flag.String("etcd-port", "2379", "etcd port")
	etcdTtl := flag.Duration("etcd-ttl", 5*time.Second, "etcd lease TTL")
	pgHost := flag.String("postgres-host", "127.0.0.1", "PostgreSQL host")
	pgPort := flag.Int("postgres-port", 5432, "PostgreSQL port")
	pbHost := flag.String("pgbouncer-host", "127.0.0.1", "PgBouncer host")
	pbPort := flag.Int("pgbouncer-port", 6432, "PgBouncer port")
	pgUser := flag.String("pguser", "postgres", "PostgreSQL user")
	addr := flag.String("listen", ":8080", "Address to listen on")
	connTimeout := flag.Duration("conn-timeout", 2*time.Second, "Connection timeout")

	flag.Parse()

	etcdCli, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{fmt.Sprintf("%s:%s", *etcdHost, *etcdPort)},
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		log.Fatal(fmt.Errorf("failed to connect to etcd: %w", err))
	}
	defer etcdCli.Close()

	sessionCtx, cancel := context.WithTimeout(context.Background(), *etcdTtl)
	defer cancel()

	session, err := concurrency.NewSession(etcdCli, concurrency.WithTTL(int(etcdTtl.Seconds())), concurrency.WithContext(sessionCtx))
	if err != nil {
		log.Fatal(err)
	}
	defer session.Close()

	election := concurrency.NewElection(session, "/my-election")

	// Observe current leader
	obsCh := election.Observe(context.Background()) // streams leadership changes

	go func() {
		for resp := range obsCh {
			fmt.Println("Leader changed to:", string(resp.Kvs[0].Value))
		}
	}()

	hostname, err := os.Hostname()
	if err != nil {
		log.Fatal(fmt.Errorf("failed to get hostname: %w", err))
	}

	go func() {
		ctx := context.Background()

		for {
			err := election.Campaign(ctx, hostname)
			if err != nil {
				log.Printf("Campaign error: %v", err)
				time.Sleep(time.Second)
				continue
			}

			// On success
			log.Println("Acquired leadership")

			// TODO: Monitor session.Done() channel to see
			// if we lost leader

			time.Sleep(10 * time.Second)

			// When done
			election.Resign(ctx)
			log.Println("Resigned leadership")

			time.Sleep(1 * time.Second)
		}
	}()

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		pgOK, pgErr := checkDB(*pgHost, *pgPort, *pgUser, *connTimeout)
		pbOK, pbErr := checkDB(*pbHost, *pbPort, *pgUser, *connTimeout)

		resp := HealthResponse{
			PostgresOK:   pgOK,
			PostgresErr:  pgErr,
			PgBouncerOK:  pbOK,
			PgBouncerErr: pbErr,
		}

		status := http.StatusOK
		if !pgOK || !pbOK {
			status = http.StatusServiceUnavailable
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(resp)
	})

	log.Printf("Listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, nil))
}
