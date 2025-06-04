#!/usr/bin/env bash

set -euo pipefail

NETWORK_BASE=10.42.0
NETWORK_CIDR_SLASH=24
NETDEV_NAME=pglab0

make_network_ip() {
    local suffix="$1"
    if [[ -z "$suffix" ]]; then
        echo "Usage: make_network_ip <suffix>"
        return 1
    fi

    echo "${NETWORK_BASE}.${suffix}"
}

make_network_ip_cidr() {
    echo "$(make_network_ip "$1")/${NETWORK_CIDR_SLASH}"
}

create_machine() {
    if [[ $# -ne 1 ]]; then
        echo "Usage: create_machine <name>"
        return 1
    fi

    local name="$1"

    args=(
        -c # Use package cache on host
        -K # Do not use the host's pacman keyring
    )

    packages=(
        base
        fish
        inetutils
        postgresql
        nano
        sudo
        zsh
    )

    local directory="/var/lib/machines/$name"
    echo "Creating Arch Linux rootfs in '$directory'"

    sudo rm -rf "$directory"
    sudo mkdir -p "$directory"

    sudo pacstrap "${args[@]}" "$directory" "${packages[@]}"

    sudo tee "$directory/etc/systemd/network/host0.network" > /dev/null <<EOF
[Match]
Name=host0

[Network]
# TODO Vary IPs per host
Address=$(make_network_ip_cidr 2)
Gateway=$(make_network_ip 1)
DNS=$(make_network_ip 1)
EOF

    sudo mkdir -p /run/systemd/nspawn
    sudo tee "/run/systemd/nspawn/$name.nspawn" > /dev/null <<EOF
[Network]
Bridge=$NETDEV_NAME

[Exec]
Boot=yes
EOF

    sudo tee "$directory/bootstrap.sh" > /dev/null <<EOF
# Don't require a password for root in the container
passwd -d root

# Enable systemd-networkd
ln -sf /run/systemd/resolve/stub-resolv.conf /etc/resolv.conf
systemctl enable systemd-networkd
systemctl enable systemd-resolved
EOF

    sudo systemd-nspawn -D "$directory" bash /bootstrap.sh
}

setup_lab_network() {
    sudo mkdir -p /run/systemd/network

    sudo tee /run/systemd/network/$NETDEV_NAME.netdev > /dev/null <<EOF
[NetDev]
Name=$NETDEV_NAME
Kind=bridge
EOF

    sudo tee /run/systemd/network/$NETDEV_NAME.network > /dev/null <<EOF
[Match]
Name=$NETDEV_NAME

[Network]
Address=$(make_network_ip_cidr 1)
DHCPServer=yes
IPForward=yes
EOF

    sudo systemctl daemon-reload # I don't think daemon-reload is necessary for network stuff
    sudo systemctl restart systemd-networkd
}

# CLI entrypoint if run directly
if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
    set -euo pipefail

    if [[ $# -ne 0 ]]; then
        "$@"
        exit
    fi

    setup_lab_network
    create_machine "pg0"
fi
