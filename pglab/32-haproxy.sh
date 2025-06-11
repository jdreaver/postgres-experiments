#!/usr/bin/env bash

setup_haproxy() {
    if [[ $# -ne 1 ]]; then
        echo "Usage: setup_haproxy <name>"
        return 1
    fi

    local name="$1"
    local directory="/var/lib/machines/$name"

    local pgbouncer_port=6432
    local pgdaemon_port=8080

    sudo tee "$directory/etc/haproxy/haproxy.cfg" > /dev/null <<EOF
global
    maxconn 100

defaults
    mode tcp
    timeout connect 4s
    timeout client 30m
    timeout server 30m
    timeout check 5s

# Stats UI
listen stats
    mode http
    bind *:7000
    stats enable
    stats uri /

# Route to the current primary
listen primary
    bind *:5432
    option httpchk OPTIONS /primary
    http-check expect status 200
    default-server inter 3s fall 2 rise 1
    server pg0 ${HOST_IPS[pg0]}:$pgbouncer_port check port $pgdaemon_port
    server pg1 ${HOST_IPS[pg1]}:$pgbouncer_port check port $pgdaemon_port
    server pg2 ${HOST_IPS[pg2]}:$pgbouncer_port check port $pgdaemon_port

# Route to all healthy nodes (including primary)
listen all
    bind *:5433
    option httpchk OPTIONS /health
    http-check expect status 200
    default-server inter 3s fall 2 rise 1
    server pg0 ${HOST_IPS[pg0]}:$pgbouncer_port check port $pgdaemon_port
    server pg1 ${HOST_IPS[pg1]}:$pgbouncer_port check port $pgdaemon_port
    server pg2 ${HOST_IPS[pg2]}:$pgbouncer_port check port $pgdaemon_port
EOF

    sudo tee "$directory/bootstrap.sh" > /dev/null <<EOF
systemctl enable haproxy.service
EOF

    sudo systemd-nspawn -D "$directory" bash /bootstrap.sh
}
