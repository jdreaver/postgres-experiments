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
apt -y install postgresql-$PG_VERSION

# Disable the default PostgreSQL service installed from the apt package
systemctl disable --now postgresql.service

# Don't use pg_ctlcluster, which is a wrapper around pg_ctl. Use pg_ctl directly.
ln -sf /usr/lib/postgresql/$PG_VERSION/bin/pg_ctl /usr/local/bin/pg_ctl

# Nuke the default PostgreSQL data
rm -rf /var/lib/postgresql/$PG_VERSION

# Use this directory for data
mkdir -p /var/lib/postgres
chown -R postgres:postgres /var/lib/postgres

# Allow postgres user to start and stop postgres
tee "/etc/sudoers.d/100-postgres" > /dev/null <<EOF
postgres ALL=(ALL) NOPASSWD: /usr/bin/systemctl start postgresql.service, /usr/bin/systemctl stop postgresql.service, /usr/bin/systemctl restart postgresql.service, /usr/bin/systemctl reload postgresql.service, /usr/bin/systemctl start pgbouncer.service, /usr/bin/systemctl stop pgbouncer.service, /usr/bin/systemctl restart pgbouncer.service, /usr/bin/systemctl reload pgbouncer.service
EOF

# Create systemd unit, overriding the one that comes with apt package.
# Taken from
# https://gitlab.archlinux.org/archlinux/packaging/packages/postgresql/-/blob/main/postgresql.service?ref_type=heads
cat <<EOF | tee /etc/systemd/system/postgresql.service
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

# ExecStartPre=/usr/bin/postgresql-check-db-dir \${PGROOT}/data
ExecStart=/usr/lib/postgresql/${PG_VERSION}/bin/postgres -D \${PGROOT}/data
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

# Set up pgbouncer
apt -y install pgbouncer
systemctl disable --now pgbouncer.service

mkdir -p /etc/pgbouncer
cat <<EOF | tee /etc/pgbouncer/pgbouncer.ini
[databases]
# Connect with Unix socket
* = host=/var/run/postgresql

[pgbouncer]
listen_addr = 0.0.0.0
listen_port = 6432
auth_type = trust
auth_file = /etc/pgbouncer/userlist.txt
pool_mode = transaction
admin_users = postgres
server_reset_query = DISCARD ALL
EOF

echo '"postgres" ""' > /etc/pgbouncer/userlist.txt
chown -R postgres:postgres /etc/pgbouncer
chmod 640 /etc/pgbouncer/userlist.txt

# Set up pgdaemon
aws s3 cp "s3://$PGLAB_USER-postgres-lab/pgdaemon" /usr/local/bin/pgdaemon
chmod +x /usr/local/bin/pgdaemon

cat <<EOF | tee /etc/systemd/system/pgdaemon.service
[Unit]
Description=Daemon for monitoring postgres

After=network.target pgbouncer.service postgresql.service

[Service]
Environment=AWS_DEFAULT_REGION=us-west-2
ExecStart=/usr/local/bin/pgdaemon -store-backend dynamodb -cluster-name my-cluster

User=postgres
Group=postgres

Restart=always
RestartSec=1s

[Install]
WantedBy=multi-user.target
EOF

# Final systemd stuff
systemctl daemon-reload
systemctl enable --now pgdaemon.service
