#!/usr/bin/env bash

set -euo pipefail

# Install apt packages
packages=(
    postgresql
)

sudo apt-get update
sudo apt-get install -y "${packages[@]}"

# Download pgdaemon
sudo aws s3 cp "s3://$PGLAB_USER-postgres-lab/pgdaemon" /usr/local/bin/pgdaemon
sudo chmod +x /usr/local/bin/pgdaemon
