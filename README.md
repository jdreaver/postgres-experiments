# Postgres Experiments

Repo where I mess around with postgres.

## TODO

pgdaemon features:
- Consider letting any node win the election if the primary fails, and have that node simply be the coordinator for fencing off the primary (make sure it is dead) and then picking the node with the smallest replica lag to be the new actual leader. (It gives up its leadership specifically to the other replica with the lowest lag.)
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

## Postgres HA

- Main patroni loop function https://github.com/patroni/patroni/blob/863bd6a07fbc591cae8663d8b916a36c00795653/patroni/ha.py#L2091
- https://www.enterprisedb.com/docs/supported-open-source/patroni/migrating/
- https://docs.percona.com/postgresql/17/solutions/ha-setup-apt.html
- https://cloud.google.com/architecture/architectures-high-availability-postgresql-clusters-compute-engine
- https://docs.aws.amazon.com/prescriptive-guidance/latest/migration-databases-postgresql-ec2/ha-postgresql-databases-ec2.html

## Leader Election

AWS/DynamoDB:
- https://aws.amazon.com/blogs/database/building-distributed-locks-with-the-dynamodb-lock-client/
- https://github.com/awslabs/amazon-dynamodb-lock-client
- https://aws.amazon.com/builders-library/leader-election-in-distributed-systems/
