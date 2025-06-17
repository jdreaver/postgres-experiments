# Postgres Experiments

Repo where I mess around with postgres.

## Goals

I'm using this repo to figure out the following:

1. How does postgres performance compare to MongoDB in steady state?
2. Is it to have a robust high availability setup with automated failover in postgres?
3. How can you migrate a MongoDB replset to postgres with at most a few seconds of downtime?

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
