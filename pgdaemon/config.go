package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"
)

type config struct {
	command       string
	etcdHost      string
	etcdPort      string
	leaseDuration time.Duration
	nodeName      string
	clusterName   string
	postgresHost  string
	postgresPort  int
	postgresUser  string
	pgBouncerHost string
	pgBouncerPort int
	listenAddress string
	primaryName   string
	replicaNames  []string
}

func parseFlags() config {
	etcdHost := flag.String("etcd-host", "127.0.0.1", "etcd host")
	etcdPort := flag.String("etcd-port", "2379", "etcd port")
	leaseDuration := flag.Duration("lease-duration", 5*time.Second, "Lease duration for leader election")
	nodeName := flag.String("node-name", "", "Name of this node in the election (defaults to hostname)")
	clusterName := flag.String("cluster-name", "", "Name of the postgres cluster")
	pgHost := flag.String("postgres-host", "127.0.0.1", "PostgreSQL host")
	pgPort := flag.Int("postgres-port", 5432, "PostgreSQL port")
	pbHost := flag.String("pgbouncer-host", "127.0.0.1", "PgBouncer host")
	pbPort := flag.Int("pgbouncer-port", 6432, "PgBouncer port")
	pgUser := flag.String("pguser", "postgres", "PostgreSQL user")
	addr := flag.String("listen", ":8080", "Address to listen on")
	primaryName := flag.String("primary-name", "", "Name of the primary node (for initialization)")
	replicaNames := flag.String("replica-names", "", "CSV of replica names (for initialization)")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: pgdaemon [command] [options]\n")
		fmt.Fprintln(os.Stderr, "Commands:")
		fmt.Fprintln(os.Stderr, "  daemon  Start the leader election daemon")
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
		command:       command,
		etcdHost:      *etcdHost,
		etcdPort:      *etcdPort,
		leaseDuration: *leaseDuration,
		nodeName:      *nodeName,
		clusterName:   *clusterName,
		postgresHost:  *pgHost,
		postgresPort:  *pgPort,
		postgresUser:  *pgUser,
		pgBouncerHost: *pbHost,
		pgBouncerPort: *pbPort,
		listenAddress: *addr,
		primaryName:   *primaryName,
		replicaNames:  strings.Split(*replicaNames, ","),
	}
}
