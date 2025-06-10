# Postgres Experiments

Repo where I mess around with postgres.

# TODO

Minor cleanups:
- Clean up `observed-state`
  - Only store data we need
  - Don't just copy names from `pg_stat_*` tables. Give names semantic meaning.
- Make reconciler logic (both leader and node) pure. Do IO before and after, but not during. (Or have an interface that does IO)
  - As much as possible, reconcilers should be like "compilers" that take state and produce actions.

Better structured logging. Use context more, like to store goroutine "name" (leader, node reconciler, health check server)

Store replication lag (accounting for 0 lag) in replica observed state (would be nice to be able to do this from primary too. Is there a way? Do we need `hot_standby_feedback`?):

    ```
    CASE
        WHEN pg_last_wal_receive_lsn() = pg_last_wal_replay_lsn() THEN 0
        ELSE EXTRACT (EPOCH FROM now() - pg_last_xact_replay_timestamp())
    END AS log_delay;
    ```

Frontend load balancer that uses pgdaemon health checks (overall health, as well as special endpoints for `/primary` and `/replica` for read/write and read-only connections)
- Can use HAProxy locally

Use https://github.com/spf13/viper to separate daemon and init command line flags and to support more configuration possibilities

pgdaemon architecture ideas:
- Dumb control loops. Each pgdaemon is a "controller" for its node, and the pgdaemon leader is the "controller" as well as "operator" for the cluster.
  - The leader can put desired node states into etcd, and each pgdaemon reconciles the desired state with the actual node state.
  - Special case of fencing: if node pgdaemon is not responding, leader can take actions to try and kill a node externally.
- Any pgdaemon can accept user commands and influence desired state by putting it into etcd, like "perform a failover" or even "perform a failover to node X"
- Leader pgdaemon performs cluster-wide operations, like determining which nodes are healthy, specifying if we should do a failover (including picking the node to fail over to based on replica lag after stopping traffic to the primary and picking the standby with the lowest lag), telling nodes to pg_rewind, running migrations, etc.
- Leader only does things that _require_ a single leader. Otherwise each node's pgdaemon performs its own actions.
- spec vs status, like k8s. Daemons can report if they are up to date with their spec.
- Consider using watches in addition to fallback loops instead of polling so aggressively. Watches can help actually decrease reaction time.
  - Or, just be smarter about loop duration. Slow down when things are healthy and speed up when not?
- Pure logic and internal state cache to support testing of decisions, state transitions, etc.
- Remember to use local clocks for measuring elapsed time.
- Have leader clear out stale node state (e.g. old nodes that have dropped)
- Record important events, either with a well known log line identifier or in etcd/DDB (like the k8s Events API). Things like health check failures, failover starts/ends, manual failover, etc.
- Rethink initialization: it would be nice to not have pgdaemon do so much of this (setting params and stuff). Maybe specify `-primary-init-script` and `-replica-init-script` args and put our logic in there. pgdaemon can just set relevant env vars for the replica script. Or we can just specify "extra config".
  - Also consider having a `postgresql-pgdaemon.conf` that gets `include`ed (or do e.g. `include_dir 'postgresql.conf.d/'`)

pgdaemon features:
- Nodes should ping one another so they can determine if etcd/DDB is down. If all nodes can be contacted, then continue as usual (sans leader elections). Especially important for primary. If primary can still contact a majority of replicas, then don't step down. If it can't, then step down.
- Write thorough tests, perhaps with a real backend, and with a mock backend with mocked time
- DynamoDB backend (just abstract common bits from etcd backend)
- Implement manual failover (not automated) so pgdaemon knows the sequence of events it must do to perform failover
  - Consider having pgdaemon implement starting `postgresql.service` as well, and do different things depending on leader vs replica

Physical vs logical replication
- "Physical replication group" is standard HA setup (1 primary, 1+ replicas).
- Anything that requires logical replication (shard splits, complex migrations, vacuum full, etc) requires logically replicating from the primary physical group to another node. This combination is a "logical replication group"
  - Once we are ready to switch over to the logically-replicated node, we can spin up 1+ replicas right beforehand, making it HA.

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
