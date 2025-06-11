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
