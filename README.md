# Postgres Experiments

Repo where I mess around with postgres.

## TODO

Try implementing my own leader election, lease, failover (use etcd or dynamodb local)

pgbouncer on every postgres node (connect to pgbouncer instead of postgres directly). Maybe even only allow local connections on pg_hba.conf (remove 0.0.0.0/0), and only allow pgbouncer connections?

Tech to investigate:
- Citus
- Patroni for HA
- Barman for backups?

# Resources

- https://www.enterprisedb.com/docs/supported-open-source/patroni/migrating/
- https://docs.percona.com/postgresql/17/solutions/ha-setup-apt.html
- https://cloud.google.com/architecture/architectures-high-availability-postgresql-clusters-compute-engine
- https://docs.aws.amazon.com/prescriptive-guidance/latest/migration-databases-postgresql-ec2/ha-postgresql-databases-ec2.html
