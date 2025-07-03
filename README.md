# Postgres Experiments

Repo where I mess around with postgres.

## Goals

I'm using this repo to figure out the following:

1. How does postgres performance compare to MongoDB in steady state for a variety of workloads and replication setups?
2. With postgres, is it possible to have a robust, high availability setup with quick, automated failover?
3. How can you migrate a MongoDB replset to postgres with at most a few seconds of downtime?

## Status

### pgdaemon

`pgdaemon` sits on each postgres node and participates in running the cluster. It manages its local postgres instance on the same node and communicates with other `pgdaemon`s primarily via a state store (etcd or DynamoDB).

The key feature of `pgdaemon` is deterministically converting cluster status (including node health) into a new desired cluster status and atomically storing that new status into the state store. This is done using a compare-and-set operation and doesn't require leader election. Nodes exclusively use local monotonic clocks to track staleness, so they don't rely on synchronized clocks.

Each `pgdaemon` fetches the desired state of the cluster and applies it to their node. For example, if the cluster is just spinning up, the local node will have to be configured as the primary or as a replica that follows a specific primary. During failover, a node will have to be reconfigured to become a primary via `pg_promote`, or follow a new primary (set `primary_conninfo`, maybe `pg_rewind`, etc).

When a cluster first starts, `pgdaemon` knows how to join itself to the cluster without central coordination. Nodes can seamlessly join the cluster at-will.

### systemd-nspawn and AWS

There are scripts to run this locally on a Linux machine using `systemd-nspawn`. The containers include multiple postgres nodes, an etcd cluster, a MongoDB cluster, an HAProxy machine, and a DynamoDB local machine.

There are also scripts and CloudFormation templates to deploy this on AWS for more realistic testing and latency.

### Load balancing and connection pooling

`pgdaemon` includes health check endpoints for determining if the current node is the primary and/or if it is healthy. Load balancers (HAProxy or an AWS NLB) can use these endpoints to route traffic to a primary or just any healthy node. Each node also has a local `pgbouncer` to pool connections.

### Future work

Raw TODOs are in [TODO.md](./TODO.md).

## Running

Run `make -j` to build and run the lab machines. Tip: you can use `make -o [rule]` to skip a rule as a dependency.

`make -j all` will also run benchmarks and other stuff. Look at the Makefile to see stuff to do.

You can connect to postgres via:
- `haproxy0` port 5432 for primary
- `haproxy0` port 5433 for any node
- Any postgres node on port 6432 for pgbouncer
- Any postgres directly on port 5432

Connect to MongoDB with `mongosh --host mongo0,mongo1,mongo2`

## Useful etcd commands

See all keys/values:

```
$ watch -n 0.1 etcdctl get '""' --prefix
```

Same, but with JSON parsing for the value

```
$ etcdctl get '' --prefix --write-out=json | jq '.kvs[] | { key: .key | @base64d, value: (.value | @base64d | try fromjson) // (.value | @base64d) }'
```

# Resources

## Postgres HA

- Main patroni loop function https://github.com/patroni/patroni/blob/863bd6a07fbc591cae8663d8b916a36c00795653/patroni/ha.py#L2091
- https://www.enterprisedb.com/docs/supported-open-source/patroni/migrating/
- https://docs.percona.com/postgresql/17/solutions/ha-setup-apt.html
- https://cloud.google.com/architecture/architectures-high-availability-postgresql-clusters-compute-engine
- https://docs.aws.amazon.com/prescriptive-guidance/latest/migration-databases-postgresql-ec2/ha-postgresql-databases-ec2.html
- [CrunchyData postgres operator](https://access.crunchydata.com/documentation/postgres-operator/latest)
- https://pg-auto-failover.readthedocs.io

## Postgres on EC2

- https://docs.aws.amazon.com/whitepapers/latest/optimizing-postgresql-on-ec2-using-ebs/optimizing-postgresql-on-ec2-using-ebs.html

## Leader Election

AWS/DynamoDB:

- https://aws.amazon.com/blogs/database/building-distributed-locks-with-the-dynamodb-lock-client/
- https://github.com/awslabs/amazon-dynamodb-lock-client
- https://aws.amazon.com/builders-library/leader-election-in-distributed-systems/
