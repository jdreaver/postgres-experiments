#!/usr/bin/env bash

set -euo pipefail

apt update

# Import the repository signing key
apt install curl ca-certificates
install -d /usr/share/postgresql-common/pgdg
curl -o /usr/share/postgresql-common/pgdg/apt.postgresql.org.asc --fail https://www.postgresql.org/media/keys/ACCC4CF8.asc

# Create the repository configuration file
. /etc/os-release
sh -c "echo 'deb [signed-by=/usr/share/postgresql-common/pgdg/apt.postgresql.org.asc] https://apt.postgresql.org/pub/repos/apt $VERSION_CODENAME-pgdg main' > /etc/apt/sources.list.d/pgdg.list"

# Install the latest version of PostgreSQL
PG_VERSION=17
apt update
apt -y install postgresql-client-$PG_VERSION

# Download pgdaemon
aws s3 cp "s3://$PGLAB_USER-postgres-lab/pgdaemon" /usr/local/bin/pgdaemon
chmod +x /usr/local/bin/pgdaemon

# Download pglab-bench
aws s3 cp "s3://$PGLAB_USER-postgres-lab/pglab-bench" /usr/local/bin/pglab-bench
chmod +x /usr/local/bin/pglab-bench
