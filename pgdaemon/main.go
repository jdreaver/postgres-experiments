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
	conf := parseFlags()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{fmt.Sprintf("%s:%s", conf.etcdHost, conf.etcdPort)},
		DialTimeout: 2 * time.Second,
	})
	if err != nil {
		log.Fatal(fmt.Errorf("failed to connect to etcd: %w", err))
	}
	defer cli.Close()

	etcd, err := NewEtcdBackend(cli, conf.clusterName, conf.nodeName, conf.leaseDuration)
	if err != nil {
		log.Fatalf("Failed to create election: %v", err)
	}

	switch conf.command {
	case "daemon":
		daemon(ctx, etcd, conf)
	default:
		flag.Usage()
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", conf.command)
		os.Exit(1)
	}
}

func daemon(ctx context.Context, etcd *EtcdBackend, conf config) {
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
				state, err := fetchPostgresNodeState(conf.postgresHost, conf.postgresPort, conf.postgresUser, 500*time.Millisecond)
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
	})

	// Wait for goroutines to exit
	if err := g.Wait(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Fatal error: %v", err)
	}
}
