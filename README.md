# Postgres Experiments

Repo where I mess around with postgres.

## TODO

pgdaemon features:
- Implement manual failover (not automated) so pgdaemon knows the sequence of events it must do to perform failover
  - Consider having pgdaemon implement starting `postgresql.service` as well, and do different things depending on leader vs replica
- Write heartbeats to etcd so others can read self-reported status during leader election
- Implement leader election

Try implementing my own leader election, lease, failover (use etcd or dynamodb local)

Tech to investigate:
- Citus
- Patroni for HA
- Barman for backups?

# Resources

- https://www.enterprisedb.com/docs/supported-open-source/patroni/migrating/
- https://docs.percona.com/postgresql/17/solutions/ha-setup-apt.html
- https://cloud.google.com/architecture/architectures-high-availability-postgresql-clusters-compute-engine
- https://docs.aws.amazon.com/prescriptive-guidance/latest/migration-databases-postgresql-ec2/ha-postgresql-databases-ec2.html
