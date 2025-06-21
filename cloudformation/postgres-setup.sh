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
PG_VERSION=17
sudo apt update
sudo apt -y install postgresql-$PG_VERSION

# Disable the default PostgreSQL service installed from the apt package
sudo systemctl disable --now postgresql.service

# Don't use pg_ctlcluster, which is a wrapper around pg_ctl. Use pg_ctl directly.
sudo ln -sf /usr/lib/postgresql/$PG_VERSION/bin/pg_ctl /usr/local/bin/pg_ctl

# Nuke the default PostgreSQL data
sudo rm -rf /var/lib/postgresql/$PG_VERSION

# Use this directory for data
sudo mkdir -p /var/lib/postgres
sudo chown -R postgres:postgres /var/lib/postgres

# Allow postgres user to start and stop postgres
sudo tee "/etc/sudoers.d/100-postgres" > /dev/null <<EOF
postgres ALL=(ALL) NOPASSWD: /usr/bin/systemctl start postgresql.service, /usr/bin/systemctl stop postgresql.service, /usr/bin/systemctl restart postgresql.service, /usr/bin/systemctl reload postgresql.service, /usr/bin/systemctl start pgbouncer.service, /usr/bin/systemctl stop pgbouncer.service, /usr/bin/systemctl restart pgbouncer.service, /usr/bin/systemctl reload pgbouncer.service
EOF

# Create systemd unit, overriding the one that comes with apt package.. Taken from
# https://gitlab.archlinux.org/archlinux/packaging/packages/postgresql/-/blob/main/postgresql.service?ref_type=heads
cat <<EOF | sudo tee /etc/systemd/system/postgresql.service
[Unit]
Description=PostgreSQL database server
Documentation=man:postgres(1)
After=network-online.target
Wants=network-online.target

[Service]
Type=notify
TimeoutSec=120
User=postgres
Group=postgres

Environment=PGROOT=/var/lib/postgres

SyslogIdentifier=postgres
PIDFile=/var/lib/postgres/data/postmaster.pid
RuntimeDirectory=postgresql
RuntimeDirectoryMode=755

# ExecStartPre=/usr/bin/postgresql-check-db-dir ${PGROOT}/data
ExecStart=/usr/lib/postgresql/${PG_VERSION}/bin/postgres -D ${PGROOT}/data
ExecReload=/bin/kill -HUP \${MAINPID}
KillMode=mixed
KillSignal=SIGINT

# Due to PostgreSQL's use of shared memory, OOM killer is often overzealous in
# killing Postgres, so adjust it downward
OOMScoreAdjust=-200

# Additional security-related features
PrivateTmp=true
ProtectHome=true
ProtectSystem=full
NoNewPrivileges=true
ProtectControlGroups=true
ProtectKernelModules=true
ProtectKernelTunables=true
PrivateDevices=true
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6
RestrictNamespaces=true
RestrictRealtime=true
SystemCallArchitectures=native

[Install]
WantedBy=multi-user.target
EOF

# Download pgdaemon
sudo aws s3 cp "s3://$PGLAB_USER-postgres-lab/pgdaemon" /usr/local/bin/pgdaemon
sudo chmod +x /usr/local/bin/pgdaemon

# Final systemd stuff
sudo systemctl daemon-reload
