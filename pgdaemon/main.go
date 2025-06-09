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

	clientv3 "go.etcd.io/etcd/client/v3"
	"golang.org/x/sync/errgroup"
)

func main() {
	etcdHost := flag.String("etcd-host", "127.0.0.1", "etcd host")
	etcdPort := flag.String("etcd-port", "2379", "etcd port")
	leaseDuration := flag.Duration("lease-duration", 5*time.Second, "Lease duration for leader election")
	nodeName := flag.String("node-name", "", "Name of this node in the election (defaults to hostname)")
	clusterName := flag.String("cluster-name", "my-cluster", "Name of the postgres cluster")
	pgHost := flag.String("postgres-host", "127.0.0.1", "PostgreSQL host")
	pgPort := flag.Int("postgres-port", 5432, "PostgreSQL port")
	pbHost := flag.String("pgbouncer-host", "127.0.0.1", "PgBouncer host")
	pbPort := flag.Int("pgbouncer-port", 6432, "PgBouncer port")
	pgUser := flag.String("pguser", "postgres", "PostgreSQL user")
	addr := flag.String("listen", ":8080", "Address to listen on")

	flag.Parse()

	if *nodeName == "" {
		hostname, err := os.Hostname()
		if err != nil {
			log.Fatal(fmt.Errorf("failed to get hostname: %w", err))
		}
		*nodeName = hostname
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{fmt.Sprintf("%s:%s", *etcdHost, *etcdPort)},
		DialTimeout: 2 * time.Second,
	})
	if err != nil {
		log.Fatal(fmt.Errorf("failed to connect to etcd: %w", err))
	}
	defer cli.Close()

	etcd, err := NewEtcdBackend(cli, *clusterName, *nodeName, *leaseDuration)
	if err != nil {
		log.Fatalf("Failed to create election: %v", err)
	}

	// Use errgroup for goroutine lifecycle management
	g, ctx := errgroup.WithContext(ctx)

	// Election loop
	g.Go(func() error {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
				// Election logic
				eCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
				err := etcd.RunElection(eCtx)
				cancel()
				if err != nil {
					log.Printf("Election error: %v", err)
				}

				// Fetch state
				state, err := fetchPostgresNodeState(*pgHost, *pgPort, *pgUser, 500*time.Millisecond)
				if err != nil {
					log.Printf("Failed to fetch Postgres node state: %v", err)
					errStr := err.Error()
					state = &PostgresNodeState{Error: &errStr}
				}

				// Write state to etcd
				wCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
				err = etcd.WriteNodeState(wCtx, state)
				cancel()
				if err != nil {
					log.Printf("Failed to write node state: %v", err)
				}
			}
		}
	})

	// HTTP server for health checks
	g.Go(func() error {
		srv := &http.Server{
			Addr: *addr,
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				timeout := 500 * time.Millisecond
				pgOK, pgErr := checkDB(*pgHost, *pgPort, *pgUser, timeout)
				pbOK, pbErr := checkDB(*pbHost, *pbPort, *pgUser, timeout)

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

		log.Printf("Listening on %s", *addr)
		return srv.ListenAndServe()
	})

	// Wait for goroutines to exit
	if err := g.Wait(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Fatal error: %v", err)
	}
}
