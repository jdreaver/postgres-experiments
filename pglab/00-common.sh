#!/usr/bin/env bash

# Common definitions and utilities used by all scripts.

SSH="ssh -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no"

NETDEV_NAME=pglab0

declare -A HOST_IPS
declare -a HOSTS

HOST_IPS[host]=10.42.0.1; HOSTS+=(host)
HOST_IPS[pg0]=10.42.0.10; HOSTS+=(pg0)
HOST_IPS[pg1]=10.42.0.11; HOSTS+=(pg1)
HOST_IPS[pg2]=10.42.0.12; HOSTS+=(pg2)
HOST_IPS[etcd0]=10.42.0.20; HOSTS+=(etcd0)
HOST_IPS[haproxy0]=10.42.0.30; HOSTS+=(haproxy0)

IP_CIDR_SLASH=24

PG_CLUSTER_NAME=my-cluster

wait_for_host_tcp() {
    local hostname="$1"
    local port="$2"

    local retries=30
    local ip="${HOST_IPS[$hostname]}"
    for ((i=0; i<retries; i++)); do
        if nc -z -w 1 "$ip" "$port"; then
            echo "Host $hostname ($ip:$port) is up."
            return 0
        fi
        echo "Waiting for host $hostname ($ip:$port) to be up..."
        sleep 1
    done

    echo "Host $hostname ($ip:$port) did not come up after $retries tries."
    return 1
}
