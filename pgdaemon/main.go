package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
)

func main() {
	conf := parseFlags()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// TODO: Support DynamoDB backend as well
	store, err := initializeEtcdStore(ctx, conf)
	if err != nil {
		log.Fatalf("Failed to initialize state store: %v", err)
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

func initializeEtcdStore(ctx context.Context, conf config) (StateStore, error) {
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

	return etcd, nil
}

func initCluster(ctx context.Context, store StateStore, conf config) {
	state := ClusterDesiredState{
		PrimaryName:  conf.primaryName,
		ReplicaNames: conf.replicaNames,
	}

	err := store.InitializeCluster(ctx, &state)
	if err != nil {
		log.Fatalf("Failed to initialize cluster: %v", err)
	}
}
