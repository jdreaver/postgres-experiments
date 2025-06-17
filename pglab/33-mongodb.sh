#!/usr/bin/env bash

setup_mongo() {
    if [[ $# -ne 1 ]]; then
        echo "Usage: setup_mongo <name>"
        return 1
    fi

    local name="$1"
    local directory="/var/lib/machines/$name"

    sudo mkdir -p "$directory/etc/mongod/"
    sudo tee "$directory/etc/mongod/mongod.conf" > /dev/null <<EOF
# for documentation of all options, see:
#   http://docs.mongodb.org/manual/reference/configuration-options/

# Where and how to store data.
storage:
  dbPath: /var/lib/mongodb
#  engine:
#  wiredTiger:

# where to write logging data.
systemLog:
  destination: syslog

# network interfaces
net:
  port: 27017
  bindIp: 0.0.0.0

replication:
  replSetName: "mydb"

# how the process runs
processManagement:
  timeZoneInfo: /usr/share/zoneinfo

#security:

#operationProfiling:

#replication:

#sharding:

## Enterprise-Only Options:

#auditLog:
EOF

    # Taken from (and modified)
    # https://github.com/mongodb/mongo/blob/master/rpm/mongod.service
    sudo tee "$directory/etc/systemd/system/mongod.service" > /dev/null <<EOF
[Unit]
Description=MongoDB Database Server
Documentation=https://docs.mongodb.org/manual
After=network-online.target
Wants=network-online.target

[Service]
User=mongodb
Group=mongodb

Environment="MONGODB_CONFIG_OVERRIDE_NOFORK=1"
Environment="GLIBC_TUNABLES=glibc.pthread.rseq=0"
# EnvironmentFile=-/etc/sysconfig/mongod
ExecStart=/usr/bin/mongod -f /etc/mongod/mongod.conf

RuntimeDirectory=mongodb

# file size
LimitFSIZE=infinity
# cpu time
LimitCPU=infinity
# virtual memory size
LimitAS=infinity
# open files
LimitNOFILE=64000
# processes/threads
LimitNPROC=64000
# locked memory
LimitMEMLOCK=infinity
# total threads (user+kernel)
TasksMax=infinity
TasksAccounting=false

# Recommended limits for mongod as specified in
# https://docs.mongodb.com/manual/reference/ulimit/#recommended-ulimit-settings

[Install]
WantedBy=multi-user.target
EOF

    sudo mkdir -p "$directory/etc/sysusers.d"
    sudo tee "$directory/etc/sysusers.d/20-mongod.conf" > /dev/null <<EOF
#Type  Name     ID  GECOS           Home
u      mongodb  -   "mongodb user"  /var/lib/mongodb
EOF

    sudo mkdir -p "$directory/etc/tmpfiles.d"
    sudo tee "$directory/etc/tmpfiles.d/20-mongo.conf" > /dev/null <<EOF
#Type Path               Mode User    Group    Age Argument
d     /var/lib/mongodb   0755 mongodb mongodb  -   -
EOF

    sudo tee "$directory/bootstrap.sh" > /dev/null <<EOF
# Start service
systemctl enable mongod.service
EOF

    sudo systemd-nspawn -D "$directory" bash /bootstrap.sh
}

init_mongo_replset() {
    wait_for_host_tcp mongo0 27017

    echo "Initializing MongoDB replica set on mongo0..."
    sudo systemd-run --machine mongo0 --quiet --pty mongosh --eval 'rs.initiate()'

    for replica in mongo1 mongo2; do
        wait_for_host_tcp "$replica" 27017
        echo "Adding $replica to the replica set..."
        sudo systemd-run --machine mongo0 --quiet --pty mongosh --eval "rs.add(\"$replica:27017\")"
    done

    # A write concern of {w: 1, j: false} is most similar to
    # synchronous_commit=off in postgres.
    echo "Setting default write concern to {w: 1, j: false}..."
    sudo systemd-run --machine mongo0 --quiet --pty mongosh --eval 'db.adminCommand({"setDefaultRWConcern": 1, "defaultWriteConcern": {"w": 1, "j": false}})'
}
