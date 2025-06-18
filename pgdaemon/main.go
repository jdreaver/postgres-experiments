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

	store, err := NewEtcdBackend(cli, conf.clusterName, conf.nodeName)
	if err != nil {
		log.Fatalf("Failed to create etcd backend: %v", err)
	}

	switch conf.command {
	case "show-cluster":
		showCluster(ctx, store)
	case "failover":
		failover(ctx, store, conf.targetPrimary)
	case "daemon":
		daemon(ctx, store, conf)
	default:
		flag.Usage()
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", conf.command)
		os.Exit(1)
	}
}

func showCluster(ctx context.Context, store StateStore) {
	state, err := store.FetchClusterState(ctx)
	if err != nil {
		log.Fatalf("Failed to fetch cluster state: %v", err)
	}

	jsonBytes, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		log.Fatalf("Failed to convert cluster state to JSON: %v", err)
	}
	fmt.Println(string(jsonBytes))
}

func failover(ctx context.Context, store StateStore, targetPrimary string) {
	if targetPrimary == "" {
		log.Fatal("Target primary node must be specified for failover")
	}

	state, err := store.FetchClusterState(ctx)
	if err != nil {
		log.Fatalf("Failed to fetch cluster state: %v", err)
	}

	newStatus := state.Status
	newStatus.IntendedPrimary = targetPrimary
	nodeName := "pgdaemon CLI"
	if _, err := WriteClusterStatusIfChanged(store, state.Status, newStatus, nodeName); err != nil {
		log.Fatalf("Failed to write cluster status: %v", err)
	}
	log.Printf("Initiated failover to %s", targetPrimary)
}

func daemon(ctx context.Context, store StateStore, conf config) {
	pgNode, err := NewPostgresNode(conf.postgresHost, conf.postgresPort, conf.postgresUser, conf.pgBouncerHost, conf.pgBouncerPort)
	if err != nil {
		log.Fatalf("Failed to create Postgres node: %v", err)
	}

	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		return nodeReconcilerLoop(ctx, store, conf, pgNode)
	})

	g.Go(func() error {
		return runHealthCheckServer(ctx, conf, pgNode)
	})

	if err := g.Wait(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Fatal error: %v", err)
	}
}
