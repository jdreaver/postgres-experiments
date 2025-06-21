package main

import (
	"flag"
	"fmt"
	"log"
	"os"
)

type config struct {
	command string

	storeBackend string

	etcdHost string
	etcdPort string

	dynamoDBTableName string
	dynamoDBEndpoint  string

	nodeName    string
	clusterName string

	postgresHost  string
	postgresPort  int
	postgresUser  string
	pgBouncerHost string
	pgBouncerPort int

	listenAddress string

	wakeupPort int

	targetPrimary string
}

func parseFlags() config {
	storeBackend := flag.String("store-backend", "etcd", "Backend to use for consensus (etcd or dynamodb)")
	etcdHost := flag.String("etcd-host", "127.0.0.1", "etcd host")
	etcdPort := flag.String("etcd-port", "2379", "etcd port")
	dynamoDBTableName := flag.String("dynamodb-table", "pgdaemon-clusters", "DynamoDB table name")
	dynamoDBEndpoint := flag.String("dynamodb-endpoint", "", "DynamoDB endpoint")
	nodeName := flag.String("node-name", "", "Name of this node")
	clusterName := flag.String("cluster-name", "", "Name of the postgres cluster")
	pgHost := flag.String("postgres-host", "127.0.0.1", "PostgreSQL host")
	pgPort := flag.Int("postgres-port", 5432, "PostgreSQL port")
	pbHost := flag.String("pgbouncer-host", "127.0.0.1", "PgBouncer host")
	pbPort := flag.Int("pgbouncer-port", 6432, "PgBouncer port")
	pgUser := flag.String("pguser", "postgres", "PostgreSQL user")
	listenAddress := flag.String("listen", "0.0.0.0:8080", "Address to listen on")
	wakeupPort := flag.Int("wakeup-port", 9090, "UDP port for wakeup packets (0 to disable)")
	targetPrimary := flag.String("target-primary", "", "Target primary node for manual failover (optional)")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: pgdaemon [command] [options]\n")
		fmt.Fprintln(os.Stderr, "Commands:")
		fmt.Fprintln(os.Stderr, "  daemon        Start the main daemon")
		fmt.Fprintln(os.Stderr, "  show-cluster  Show current cluster state")
		fmt.Fprintln(os.Stderr, "  failover      Perform failover to -target-primary or any replica if unspecified")
		fmt.Fprintln(os.Stderr, "Options:")
		flag.PrintDefaults()
	}

	flag.Parse()

	command := flag.Arg(0)
	if command == "" {
		command = "daemon"
	}

	if *nodeName == "" {
		hostname, err := os.Hostname()
		if err != nil {
			log.Fatal(fmt.Errorf("failed to get hostname: %w", err))
		}
		*nodeName = hostname
	}

	if *clusterName == "" {
		log.Fatal("Cluster name must be specified with -cluster-name")
	}

	return config{
		command: command,

		storeBackend: *storeBackend,

		etcdHost: *etcdHost,
		etcdPort: *etcdPort,

		dynamoDBTableName: *dynamoDBTableName,
		dynamoDBEndpoint:  *dynamoDBEndpoint,

		nodeName:    *nodeName,
		clusterName: *clusterName,

		postgresHost:  *pgHost,
		postgresPort:  *pgPort,
		postgresUser:  *pgUser,
		pgBouncerHost: *pbHost,
		pgBouncerPort: *pbPort,

		listenAddress: *listenAddress,

		wakeupPort: *wakeupPort,

		targetPrimary: *targetPrimary,
	}
}
