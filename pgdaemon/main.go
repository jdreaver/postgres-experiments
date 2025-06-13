package main

import (
	"context"
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

	// TODO: Support DynamoDB backend as well
	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{fmt.Sprintf("%s:%s", conf.etcdHost, conf.etcdPort)},
		DialTimeout: 2 * time.Second,
	})
	if err != nil {
		log.Fatal(fmt.Errorf("failed to connect to etcd: %w", err))
	}
	defer cli.Close()

	store, err := NewEtcdBackend(cli, conf.clusterName, conf.nodeName, conf.leaseDuration)
	if err != nil {
		log.Fatalf("Failed to create election: %v", err)
	}

	switch conf.command {
	case "init-cluster":
		initCluster(ctx, store, conf)
	case "daemon":
		daemon(ctx, store, conf)
	default:
		flag.Usage()
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", conf.command)
		os.Exit(1)
	}
}

func initCluster(ctx context.Context, store StateStore, conf config) {
	spec := ClusterSpec{
		PrimaryName:  conf.primaryName,
		ReplicaNames: conf.replicaNames,
	}

	err := store.InitializeCluster(ctx, &spec)
	if err != nil {
		log.Fatalf("Failed to initialize cluster: %v", err)
	}
}

func daemon(ctx context.Context, store StateStore, conf config) {
	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		return leaderReconcilerLoop(ctx, store)
	})

	g.Go(func() error {
		return nodeReconcilerLoop(ctx, store, conf)
	})

	g.Go(func() error {
		return runHealthCheckServer(ctx, conf)
	})

	if err := g.Wait(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Fatal error: %v", err)
	}
}
