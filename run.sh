#!/usr/bin/env bash

set -euo pipefail

SSH="ssh -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no"

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

create_pgbase_machine() {
    pacstrap_args=(
        -c # Use package cache on host
        -K # Do not use the host's pacman keyring
    )

    packages=(
        base
        postgresql
        openssh

        bat
        eza
        fish
        inetutils
        less
        nano
        procs
        ripgrep
        sudo
        zsh
    )

    local directory="/var/lib/machines/pgbase"
    echo "Creating Arch Linux rootfs in '$directory'"

    sudo rm -rf "$directory"
    sudo mkdir -p "$directory"

    sudo pacstrap "${pacstrap_args[@]}" "$directory" "${packages[@]}"

    sudo tee "$directory/bootstrap.sh" > /dev/null <<EOF
# Don't require a password for root in the container
passwd -d root

# Use /usr/bin/fish as login shell for root
chsh -s /usr/bin/fish root

# Enable systemd-networkd
ln -sf /run/systemd/resolve/stub-resolv.conf /etc/resolv.conf
systemctl enable systemd-networkd
systemctl enable systemd-resolved

# Locale
sed -i 's/^#\(en_US.UTF-8\)/\1/' /etc/locale.gen
locale-gen
echo 'LANG=en_US.UTF-8' >/etc/locale.conf

# Set up postgres
sudo -u postgres initdb --locale=C.UTF-8 --encoding=UTF8 -D /var/lib/postgres/data
systemctl enable postgresql.service

# Allow connections from all hosts, without password
echo "host    all             all             0.0.0.0/0            trust" >> /var/lib/postgres/data/pg_hba.conf

# Allow replication from all hosts
echo "host    replication     all             0.0.0.0/0            trust" >> /var/lib/postgres/data/pg_hba.conf

# Bind to all interfaces
echo "listen_addresses = '*'" >> /var/lib/postgres/data/postgresql.conf

# More logging
echo 'log_connections = on' >> /var/lib/postgres/data/postgresql.conf
echo 'log_hostname = on' >> /var/lib/postgres/data/postgresql.conf

# SSH
sed -i 's/^#\?PermitRootLogin.*/PermitRootLogin yes/' /etc/ssh/sshd_config
sed -i 's/^#\?PermitEmptyPasswords.*/PermitEmptyPasswords yes/' /etc/ssh/sshd_config
sed -i 's/^#\?PasswordAuthentication.*/PasswordAuthentication yes/' /etc/ssh/sshd_config
systemctl enable sshd.service
EOF

    sudo systemd-nspawn -D "$directory" bash /bootstrap.sh
}

create_machine() {
    if [[ $# -ne 2 ]]; then
        echo "Usage: create_machine <name> <ip_suffix>"
        return 1
    fi

    local name="$1"
    local ip_suffix="$2"

    local directory="/var/lib/machines/$name"

    sudo rm -rf "$directory"
    sudo cp --archive /var/lib/machines/pgbase "$directory"

    # N.B. Network file must start with 10- to be loaded before
    # /usr/lib/systemd/network/80-container-host0.network
    sudo tee "$directory/etc/systemd/network/10-host0.network" > /dev/null <<EOF
[Match]
Name=host0

[Network]
Address=$(make_network_ip_cidr "$ip_suffix")
Gateway=$(make_network_ip 1)
DNS=$(make_network_ip 1)
DHCP=no
EOF

    sudo mkdir -p /run/systemd/nspawn
    sudo tee "/run/systemd/nspawn/$name.nspawn" > /dev/null <<EOF
[Network]
Bridge=$NETDEV_NAME

[Exec]
Boot=yes
EOF

    echo "$name" | sudo tee "$directory/etc/hostname"
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
IPForward=yes
EOF

    sudo systemctl daemon-reload # I don't think daemon-reload is necessary for network stuff
    sudo systemctl restart systemd-networkd
}

setup_replication() {
    if [[ $# -ne 2 ]]; then
        echo "Usage: setup_replication <leader-ip> <follower-ip>"
        return 1
    fi

    local leader="$1"
    local follower="$2"

    set -x

    $SSH "root@$follower" "bash -c \"
set -euo pipefail

systemctl stop postgresql.service
rm -rf /var/lib/postgres/data/* || true
sudo -u postgres pg_basebackup -d 'host=$leader user=postgres' -D /var/lib/postgres/data -R -P
systemctl start postgresql.service
systemctl status postgresql.service
\""

}

# CLI entrypoint if run directly
if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
    set -euo pipefail

    if [[ $# -ne 0 ]]; then
        "$@"
        exit
    fi

    setup_lab_network
    create_pgbase_machine
    create_machine "pg0" 2
    create_machine "pg1" 3

    sudo machinectl start pg0
    sudo machinectl start pg1

    echo "Waiting for startup"
    sleep 5

    setup_replication 10.42.0.2 10.42.0.3
fi
