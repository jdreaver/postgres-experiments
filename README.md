# Postgres Experiments

Repo where I mess around with postgres.

## Running

Run `make -j` to build the lab and run benchmarks. Tip: you can use `make -o [rule]` to skip a rule as a dependency.

# TODO

Makefile:
- Proper way to wait for etcd to be ready before initializing state, and proper way to wait for the cluster to be ready before accepting connections. Ideally this is a separate rule that imdb and pgbench can depend on, and then we can use `-o` to skip deps when we want.

Document what I've done so far. Maybe with some nice ASCII art.

Run MongoDB locally too.
- Get benchmark MongoDB data set and get it replicated into postgres so we can compare apples to apples

Add tests! Ideally reconciliation logic is pure enough we can test, and/or use a mock backend

Implement actual failover when primary changes
- Leader should pick replica with the highest `written_lsn` to be the new primary

Make distinction between "can't connect to postgres" and "my queries failed".

Better structured logging. Use context more, like to store goroutine "name" (leader, node reconciler, health check server)

(This might not be right. We need _received_ timestamp, not replay) We can use `write_lag` from primary) Store replication lag (accounting for 0 lag) in replica observed state:

    ```
    SELECT CASE
        WHEN pg_last_wal_receive_lsn() = pg_last_wal_replay_lsn() THEN 0
        ELSE EXTRACT (EPOCH FROM now() - pg_last_xact_replay_timestamp())
    END AS log_delay;
    ```

Use https://github.com/spf13/viper to separate daemon and init command line flags and to support more configuration possibilities

Investigate why inserting imdb data is so much slower. Used to take like 1.5 minutes total, now it is like 8 minutes. Replicas? Vacuuming?

pgdaemon architecture ideas:
- Any pgdaemon can accept user commands and influence desired state by putting it into etcd, like "perform a failover" or even "perform a failover to node X"
- Be smarter about loop duration. Slow down when things are healthy and speed up when not?
- Have leader clear out stale node state
  - Warn if we are seeing a node with a recent observed-state but that shouldn't be in the cluster
- Record important events, either with a well known log line identifier or in etcd/DDB (like the k8s Events API). Things like health check failures, failover starts/ends, manual failover, etc.
- Rethink PGDATA initialization: it would be nice to not have pgdaemon do so much of this (setting params and stuff). Maybe specify `-primary-init-script` and `-replica-init-script` args and put our logic in there. pgdaemon can just set relevant env vars for the replica script. Or we can just specify "extra config".
  - Also consider having a `postgresql-pgdaemon.conf` that gets `include`ed (or do e.g. `include_dir 'postgresql.conf.d/'`)
- Make reconciler logic (both leader and node) pure. Do IO before and after, but not during. (Or have an interface that does IO)
  - As much as possible, reconcilers should be like "compilers" that take state and produce actions.

pgdaemon features:
- Nodes should ping one another so they can determine if etcd/DDB is down. If all nodes can be contacted, then continue as usual (sans leader elections). Especially important for primary. If primary can still contact a majority of replicas, then don't step down. If it can't, then step down.
- Write thorough tests, perhaps with a real backend, and with a mock backend with mocked time
- DynamoDB backend (just abstract common bits from etcd backend)

Physical vs logical replication
- "Physical replication group" is standard HA setup (1 primary, 1+ replicas).
- Anything that requires logical replication (shard splits, complex migrations, vacuum full, etc) requires logically replicating from the primary physical group to another node. This combination is a "logical replication group"
  - Once we are ready to switch over to the logically-replicated node, we can spin up 1+ replicas right beforehand, making it HA.

Monitoring and some sort of dashboard to get a birds-eye-view of cluster instead of having to `journalctl -f` in many terminals.

Settings to investigate:
- `recovery_target_*` stuff https://www.postgresql.org/docs/current/runtime-config-wal.html#RUNTIME-CONFIG-WAL-RECOVERY-TARGET
- `hot_standby_feedback`, specifically for chained logical replication https://www.postgresql.org/docs/current/runtime-config-replication.html#RUNTIME-CONFIG-REPLICATION-STANDBY

## Comparison with Mongo

Compare replication and replica commit settings apples to apples with Mongo `{w: 1, j:0}`

## EC2

Get this running in AWS

EBS supports atomic writes of up to 16 kB, so we can probably turn off `full_page_writes`. Many instance store SSD volumes also support this.

# Resources

## Postgres HA

- Main patroni loop function https://github.com/patroni/patroni/blob/863bd6a07fbc591cae8663d8b916a36c00795653/patroni/ha.py#L2091
- https://www.enterprisedb.com/docs/supported-open-source/patroni/migrating/
- https://docs.percona.com/postgresql/17/solutions/ha-setup-apt.html
- https://cloud.google.com/architecture/architectures-high-availability-postgresql-clusters-compute-engine
- https://docs.aws.amazon.com/prescriptive-guidance/latest/migration-databases-postgresql-ec2/ha-postgresql-databases-ec2.html
- [CrunchyData postgres operator](https://access.crunchydata.com/documentation/postgres-operator/latest)

## Leader Election

AWS/DynamoDB:

- https://aws.amazon.com/blogs/database/building-distributed-locks-with-the-dynamodb-lock-client/
- https://github.com/awslabs/amazon-dynamodb-lock-client
- https://aws.amazon.com/builders-library/leader-election-in-distributed-systems/

## Useful etcd commands

See all keys/values:

```
$ watch -n 0.1 etcdctl get '""' --prefix
```

Same, but with JSON parsing for the value

```
$ etcdctl get '' --prefix --write-out=json | jq '.kvs[] | { key: .key | @base64d, value: (.value | @base64d | try fromjson) // (.value | @base64d) }'
```
