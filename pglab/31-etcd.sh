#!/usr/bin/env bash

setup_etcd() {
    if [[ $# -ne 1 ]]; then
        echo "Usage: setup_etcd <name>"
        return 1
    fi

    local name="$1"
    local directory="/var/lib/machines/$name"

    # Taken from
    # https://github.com/etcd-io/etcd/blob/main/contrib/systemd/etcd.service
    # and
    # https://github.com/etcd-io/etcd/blob/main/contrib/systemd/sysusers.d/20-etcd.conf

    sudo tee "$directory/etc/systemd/system/etcd.service" > /dev/null <<EOF
[Unit]
Description=etcd key-value store
Documentation=https://github.com/etcd-io/etcd
After=network-online.target local-fs.target remote-fs.target time-sync.target
Wants=network-online.target local-fs.target remote-fs.target time-sync.target

[Service]
User=etcd
Type=notify
Environment=ETCD_DATA_DIR=/var/lib/etcd
Environment=ETCD_NAME=%m
Environment=ETCD_LISTEN_CLIENT_URLS="http://0.0.0.0:2379"
Environment=ETCD_LISTEN_PEER_URLS="http://0.0.0.0:2380"
Environment=ETCD_ADVERTISE_CLIENT_URLS="http://%H:2379,https://%H:4001,http://${HOST_IPS[$name]}:2379,https://${HOST_IPS[$name]}:4001"
ExecStart=/usr/bin/etcd
Restart=always
RestartSec=10s
LimitNOFILE=40000

[Install]
WantedBy=multi-user.target
EOF

    sudo mkdir -p "$directory/etc/sysusers.d"
    sudo tee "$directory/etc/sysusers.d/20-etcd.conf" > /dev/null <<EOF
# etcd - https://github.com/etcd-io/etcd

#Type  Name  ID  GECOS        Home
u      etcd  -   "etcd user"  /var/lib/etcd
EOF

    sudo mkdir -p "$directory/etc/tmpfiles.d"
    sudo tee "$directory/etc/tmpfiles.d/20-etcd.conf" > /dev/null <<EOF
#Type Path            Mode User Group Age Argument…
d     /var/lib/etcd   0755 etcd etcd  -   -
EOF

    sudo tee "$directory/bootstrap.sh" > /dev/null <<EOF
# Start service
systemctl enable etcd.service
EOF

    sudo systemd-nspawn -D "$directory" bash /bootstrap.sh
}

initialize_cluster_state() {
    # Wait for etcd to be ready
    while ! nc -z -w 1 "${HOST_IPS[etcd0]}" 2379; do
        echo "Waiting for etcd0 to be healthy..."
        sleep 1
    done

    echo "Initializing cluster state in etcd0"
    go run -C pgdaemon . -etcd-host etcd0 -primary-name pg0 -replica-names pg1,pg2 -cluster-name my-cluster init-cluster
}
