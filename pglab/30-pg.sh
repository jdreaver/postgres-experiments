#!/usr/bin/env bash

setup_postgres() {
    if [[ $# -ne 1 ]]; then
        echo "Usage: setup_postgres <name>"
        return 1
    fi

    local name="$1"
    local directory="/var/lib/machines/$name"

    # Allow postgres user to start and stop postgres
    sudo tee "$directory/etc/sudoers.d/100-postgres" > /dev/null <<EOF
postgres ALL=(ALL) NOPASSWD: /usr/bin/systemctl start postgresql.service, /usr/bin/systemctl stop postgresql.service, /usr/bin/systemctl restart postgresql.service, /usr/bin/systemctl reload postgresql.service, /usr/bin/systemctl start pgbouncer.service, /usr/bin/systemctl stop pgbouncer.service, /usr/bin/systemctl restart pgbouncer.service, /usr/bin/systemctl reload pgbouncer.service
EOF

    sudo mkdir -p "$directory/etc/pgbouncer"
    sudo tee "$directory/etc/pgbouncer/pgbouncer.ini" > /dev/null <<EOF
[databases]
* = host=127.0.0.1 port=5432

[pgbouncer]
listen_addr = 0.0.0.0
listen_port = 6432
auth_type = trust
auth_file = /etc/pgbouncer/userlist.txt
pool_mode = transaction
admin_users = postgres
server_reset_query = DISCARD ALL
EOF

    sudo cp "$SCRIPT_DIR/pgdaemon/pgdaemon" "$directory/usr/bin/pgdaemon"
    sudo tee "$directory/etc/systemd/system/pgdaemon.service" > /dev/null <<EOF
[Unit]
Description=Daemon for monitoring postgres

After=network.target pgbouncer.service postgresql.service

[Service]
ExecStart=/usr/bin/pgdaemon -etcd-host etcd0 -cluster-name $PG_CLUSTER_NAME
User=postgres
Group=postgres
Restart=always
RestartSec=1s

[Install]
WantedBy=multi-user.target
EOF

    sudo tee "$directory/bootstrap.sh" > /dev/null <<EOF
# pgbouncer
echo '"postgres" ""' > /etc/pgbouncer/userlist.txt
chown -R pgbouncer:pgbouncer /etc/pgbouncer
chmod 640 /etc/pgbouncer/userlist.txt

# pgdaemon
systemctl enable pgdaemon.service
EOF

    sudo systemd-nspawn -D "$directory" bash /bootstrap.sh
}

run_pgbench() {
    if [[ $# -ne 1 ]]; then
        echo "Usage: run_pgbench <leader>"
        return 1
    fi

    local leader="$1"
    wait_for_host_tcp "$leader" 5432

    local leader_ip="${HOST_IPS[$leader]}"

    # Initialize with scale factor -s
    pgbench -h "$leader_ip" -U postgres -i -s 50 postgres

    # Run pgbench for -T seconds with -c clients and -j threads
    pgbench -h "$leader_ip" -U postgres -c 10 -j 4 -T 10 postgres
}

build_pgdaemon() {
    go build -C "$SCRIPT_DIR/pgdaemon" || {
        echo "Failed to build pgdaemon"
        return 1
    }
}
