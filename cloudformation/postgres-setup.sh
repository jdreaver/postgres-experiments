#!/usr/bin/env bash

set -euo pipefail

sudo apt update

# Automated config (not needed, TODO deleteme):
# sudo apt install -y postgresql-common
# sudo /usr/share/postgresql-common/pgdg/apt.postgresql.org.sh -y

# Import the repository signing key
sudo apt install curl ca-certificates
sudo install -d /usr/share/postgresql-common/pgdg
sudo curl -o /usr/share/postgresql-common/pgdg/apt.postgresql.org.asc --fail https://www.postgresql.org/media/keys/ACCC4CF8.asc

# Create the repository configuration file
. /etc/os-release
sudo sh -c "echo 'deb [signed-by=/usr/share/postgresql-common/pgdg/apt.postgresql.org.asc] https://apt.postgresql.org/pub/repos/apt $VERSION_CODENAME-pgdg main' > /etc/apt/sources.list.d/pgdg.list"

# Install the latest version of PostgreSQL
sudo apt update
sudo apt -y install postgresql-17

# Disable the default PostgreSQL service installed from the apt package
sudo systemctl disable --now postgresql.service

# Download pgdaemon
sudo aws s3 cp "s3://$PGLAB_USER-postgres-lab/pgdaemon" /usr/local/bin/pgdaemon
sudo chmod +x /usr/local/bin/pgdaemon
