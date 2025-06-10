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
	case "init-cluster":
		initCluster(ctx, etcd, conf)
	case "daemon":
		daemon(ctx, etcd, conf)
	default:
		flag.Usage()
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", conf.command)
		os.Exit(1)
	}
}

func initCluster(ctx context.Context, etcd *EtcdBackend, conf config) {
	state := ClusterDesiredState{
		PrimaryName:  conf.primaryName,
		ReplicaNames: conf.replicaNames,
	}

	err := etcd.InitializeCluster(ctx, &state)
	if err != nil {
		log.Fatalf("Failed to initialize cluster: %v", err)
	}
}
