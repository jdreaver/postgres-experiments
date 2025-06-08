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
	connTimeout := flag.Duration("conn-timeout", 2*time.Second, "Connection timeout")

	flag.Parse()

	if *nodeName == "" {
		hostname, err := os.Hostname()
		if err != nil {
			log.Fatal(fmt.Errorf("failed to get hostname: %w", err))
		}
		*nodeName = hostname
	}

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

	go func() {
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			// Leader election
			err := etcd.RunElection(ctx)
			if err != nil {
				log.Printf("Election error: %v", err)
			}

			// Write node state
			state, err := fetchPostgresNodeState(*pgHost, *pgPort, *pgUser, 2*time.Second)
			if err != nil {
				log.Printf("Failed to fetch Postgres node state: %v", err)
				errString := err.Error()
				state = &PostgresNodeState{
					Error: &errString,
				}
			}

			err = etcd.WriteNodeState(ctx, state)
			if err != nil {
				log.Printf("Failed to write node state: %v", err)
			}

			time.Sleep(1 * time.Second)
		}
	}()

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		pgOK, pgErr := checkDB(*pgHost, *pgPort, *pgUser, *connTimeout)
		pbOK, pbErr := checkDB(*pbHost, *pbPort, *pgUser, *connTimeout)

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
	})

	log.Printf("Listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, nil))
}
