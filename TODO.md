# TODO

Document what I've done so far. Maybe with some nice ASCII art.

Pure logic (both for election and for state):
- Store previous spec/status and current spec/status, as well as time diff between them.
- Using prev/current state, spit out actions to take
- Add a ton of tests on this logic

Failover:
- Plan and refactors:
  - Add a "cluster state", not just desired state. Put under `/cluster/observed-state` and move desired state under `/cluster/desired-state`
  - Dirty failover: very simple. Just do `pg_promote(wait => true)` or `ALTER SYSTEM SET primary_conninfo = '...'` + `SELECT pg_reload_conf();`
  - State machine with state
  - Make reconciliation logic pure
  - Stop primary writes (but not primary process!) and wait for a secondary to catch up (with timeout) before failing over
  - Detect degradation automatically and fail over
- First to manual failover where we select new primary with pgdaemon
  - Rename `init` command to something else, like `set-cluster-state`
- Failover process:
  - Cluster states: `provisioning`, `healthy`, `failing-over`, `unhealthy`?
  - Leader detects desired primary does not match current primary. Sets cluster to a `failover` state.
  - While in `failover` state, primary queries are canceled and inbound traffic is stopped and we wait for replicas to catch up (with a configurable timeout)
    - Can do canceling/stopping in v2 of failover
  - Then primary is stopped and the replica that is most up to date is selected as new primary
  - New primary will stop replication
  - Replicas will point to new primary

- Automated failover based on health signals

Run MongoDB locally too.
- Get benchmark MongoDB data set and get it replicated into postgres so we can compare apples to apples

Add tests! Ideally reconciliation logic is pure enough we can test, and/or use a mock backend

Implement actual failover when primary changes
- Leader should pick replica with the highest `written_lsn` to be the new primary

Make distinction between "can't connect to postgres" and "my queries failed".

Better structured logging. Use context more, like to store goroutine "name" (leader, node reconciler, health check server)

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
