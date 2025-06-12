# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build and Development Commands

- `make -j` - Build the lab environment and run benchmarks
- `make machines` - Create all VMs (postgres, etcd, haproxy, mongo) and initialize cluster
- `make -o [rule]` - Skip a rule as dependency (useful for development)
- `cd pgdaemon && go build` - Build the pgdaemon Go binary
- `cd pgdaemon && go run . daemon -node [name]` - Run pgdaemon in daemon mode

## Architecture Overview

This is a PostgreSQL high availability experimentation lab using systemd-nspawn containers to simulate a distributed cluster environment.

### Core Components

**pgdaemon** - Go-based PostgreSQL HA daemon that provides:
- Leader election using etcd
- Automatic failover and cluster management with replica lag monitoring
- Health monitoring and reconciliation loops
- Supports both init-cluster and daemon modes
- Configurable failover catch-up timeout (`--failover-timeout`)

**Lab Infrastructure** (pglab/ scripts):
- Creates isolated network (10.42.0.0/24) with systemd-nspawn containers
- Manages postgres nodes (pg0, pg1, pg2), etcd cluster, haproxy load balancer
- Includes MongoDB replicas for performance comparison
- Automated IMDB dataset loading for benchmarking

### Key Architecture Patterns

**State Management**: Uses etcd as distributed state store with desired vs observed state reconciliation
**Leader Election**: Leader daemon manages cluster-wide decisions while node daemons handle local postgres instances
**Health Monitoring**: HTTP health endpoints on each node for external monitoring
**Reconciliation Loops**: Separate goroutines for leader reconciliation, node reconciliation, and health checking
**Failover Process**: Leader detects primary failures, selects replica with highest written LSN, optionally waits for catch-up, then promotes best replica

### Development Workflow

1. `make machines` to create lab environment
2. Modify pgdaemon code
3. `cd pgdaemon && go build` to rebuild daemon
4. Deploy to containers via run.sh functions
5. Use `etcdctl get '' --prefix` to inspect cluster state
6. Monitor with `journalctl -f` on individual containers

### Key Files

- `pgdaemon/` - HA daemon implementation in Go
- `pglab/` - Lab setup scripts (network, containers, services)
- `run.sh` - Main orchestration script that sources all pglab functions
- `Makefile` - Build automation and target definitions
- `imdb-data/` - Benchmark datasets and schema
