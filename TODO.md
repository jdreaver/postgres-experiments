# TODO

Get rid of leader election loop, and make TODO to nuke all leader election code.

Write benchmark program in Go:
- Use a synthetic dataset.
- MongoDB probably only has one storage format (everything squished in a document), while postgres can either do tables or id/jsonb.
- Varying number of indexes?
- With or without foreign keys
- Transactions or not in postgres
- Also measure how much downtime we incur during failover, and how well we can continue on while retrying queries

Document what I've done so far. Maybe with some nice ASCII art.

Failover plan:
- State machine
  - Prefactor: much of what is currently in the cluster spec should actually be cluster status. Primary and replicas will change over time!
  - Declare states like `healthy`, `waiting_for_catchup`, `selecting_new_primary`, `shutting_down_old`, `promoting_new`, `reconfiguring_replicas`, etc
  - Have a pure function that transitions states
    - Idea: re-run the function in the same loop over and over until the state stops changing. It is possible we transition through multiple states quickly, like if a replica is already caught up (so no need to wait for catchup, and we can immediately select a replica too)
  - See my TODO below about using UDP packets to inform all other pgdaemons about state changes so they can act now instead of waiting for the next loop
- Dirty failover is _too_ dirty. Need a bit of coordination (shut down primary, allow catchup, etc). Seeing too much WAL divergence because of race conditions.
  - Have replicas wait until new primary is reporting as a primary before trying to connect to it
  - Be more careful with terminating walreceiver. Maybe detect if we have to and only do it if necessary (investigate when this is necessary)
- Read node state, making distinction between "can't connect to node" and "failed to run query"

Pure logic (both for election and for state):
- Add a ton of tests on this logic
- Store previous spec/status and current spec/status, as well as time diff between them.
- Using prev/current state, spit out actions to take

Testing:
- Integration test that performs a couple failovers and postgres queries work (through HAProxy), all nodes are reporting to etcd, all nodes are healthy, replication is working, etc
- Test `runInner` with mocked backend for leader election
- Property tests that run "actions" sorted by time for leader election. Assert we have at most one leader at a time (no more than one node _thinks_ they are leader)

Rethink PGDATA initialization: it would be nice to not have pgdaemon do so much of this (setting params and stuff). Maybe specify `-primary-init-script` and `-replica-init-script` args and put our logic in there. pgdaemon can just set relevant env vars for the replica script. Or we can just specify "extra config".

Failover:
- Plan and refactors:
  - Stop primary writes (but not primary process!) and wait for a secondary to catch up (with timeout) before failing over
  - Detect degradation automatically and fail over
- First to manual failover where we select new primary with pgdaemon
- Failover process:
  - Cluster states: `provisioning`, `healthy`, `failing-over`, `unhealthy`?
  - Leader detects desired primary does not match current primary. Sets cluster to a `failover` state.
  - While in `failover` state, primary queries are canceled and inbound traffic is stopped and we wait for replicas to catch up (with a configurable timeout)
    - Can do canceling/stopping in v2 of failover
  - Then primary is stopped and the replica that is most up to date is selected as new primary
  - New primary will stop replication
  - Replicas will point to new primary
- Automated failover based on health signals
  - If the primary loses a connection to the majority of replicas, it should step down (network partition)
  - If a majority of secondaries agree they cannot connect to the primary, a new primary should be nominated (could be dead primary, could be network partition)

Nodes joining cluster:
- Consider putting timeout on nodes trying to join cluster so they fail if they are never allowed to join.
- Leader can spit out an Event for rejecting nodes so we have clear logging.
- Set max number of nodes as config option. This is mainly so misconfiguration doesn't bring cluster down.

Events system:
- Record important events, either with a well known log line identifier or in etcd/DDB (like the k8s Events API). Things like health check failures, failover starts/ends, manual failover, etc.
- We can flush events to postgres if we want, to keep etcd clear.

Use a replication slot per replica so we don't lose WAL https://www.postgresql.org/docs/current/warm-standby.html#STREAMING-REPLICATION-SLOTS
- It might actually be nice for replicas to be able to check that the slot is ready before trying to boot.
- If we do this, then maybe lower `wal_keep_size`?
- Detect if replica cannot possibly catch up because `SELECT pg_last_wal_replay_lsn();` on the replica is older than `SELECT restart_lsn FROM pg_replication_slots;` for that replica's slot on the primary

Use postgres system identifier to identify the cluster https://pgpedia.info/d/database-system-identifier.html
- All nodes should share this identifier. Find a way to abort if a local node's identifier is different (except before it tries to join the cluster)
- Patroni uses this https://patroni.readthedocs.io/en/latest/faq.html#dcs

Re-evaluate lease-based leader election. We can't ever guarantee there is only a single leader.
- Perhaps "leader election" can be atomic compare-and-swaps for deciding cluster state, without needing a single leader that holds a lease. Each node can evaluate its state of the world and attempt to atomically write desired cluster state. The desired cluster state could itself be the "lease" (e.g. don't attempt to change state until lease expires, but no node "holds" the lease)

TLA+ or Quint to model out leader election in isolation and leader election + failover
- https://learntla.com/
- https://quint-lang.org/docs/why
- Use the model to inform tests in the code (unit tests, integration tests, randomized/property tests, etc)

Run MongoDB locally too.
- Get benchmark MongoDB data set and get it replicated into postgres so we can compare apples to apples

Make distinction between "can't connect to postgres" and "my queries failed".

Logging
- Use log levels that systemd understands so we can filter out INFO logs sometimes (any significant events can be a higher level, maybe)
- Structured logging: Use context more, like to store goroutine "name" (leader, node reconciler, health check server)

Use https://github.com/spf13/viper to separate daemon and init command line flags and to support more configuration possibilities

Investigate why inserting imdb data is so much slower sometimes. Used to take like 1.5 minutes total, now it is like 8 minutes. Replicas? Vacuuming? It is intermittent.

Be smarter about loop duration to save costs. Slow down when things are healthy and speed up when not?
- We could have a loop duration we use when everything is fine (say, 3s) and a faster duration if anything is unhealthy or not converged to desired state (say, 0.5s)
- pgdaemons could listen on a UDP port for a "wakeup" that other nodes could fire off to all nodes when something significant happens, like a state change, or health degradation

Have leader clear out stale node state
- Warn if we are seeing a node with a recent observed-state but that shouldn't be in the cluster

Nodes should ping one another so they can determine if etcd/DDB is down. If all nodes can be contacted, then continue as usual (sans leader elections). Especially important for primary. If primary can still contact a majority of replicas, then don't step down. If it can't, then step down.

DynamoDB backend (just abstract common bits from etcd backend)

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

https://docs.aws.amazon.com/whitepapers/latest/optimizing-postgresql-on-ec2-using-ebs/optimizing-postgresql-on-ec2-using-ebs.html
