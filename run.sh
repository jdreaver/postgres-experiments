#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR=$(dirname "${BASH_SOURCE[0]}")

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

setup_postgres() {
    if [[ $# -ne 1 ]]; then
        echo "Usage: setup_postgres <name>"
        return 1
    fi

    local name="$1"
    local directory="/var/lib/machines/$name"

    sudo tee "$directory/bootstrap.sh" > /dev/null <<EOF
# Initialize data and start service
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

# More settings
echo 'synchronous_commit = off' >> /var/lib/postgres/data/postgresql.conf
echo 'work_mem = 64MB' >> /var/lib/postgres/data/postgresql.conf
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

run_pgbench() {
    if [[ $# -ne 1 ]]; then
        echo "Usage: run_pgbench <leader-ip"
        return 1
    fi

    local leader="$1"

    # Initialize with scale factor -s
    pgbench -h "$leader" -U postgres -i -s 50 postgres

    # Run pgbench for -T seconds with -c clients and -j threads
    pgbench -h "$leader" -U postgres -c 10 -j 4 -T 10 postgres
}

download_imdb_datasets() {
    local data_dir="$SCRIPT_DIR/imdb-data"
    mkdir -p "$data_dir"

    fetch_imdb() {
        local name="$1"
        local url="https://datasets.imdbws.com/${name}.tsv.gz"
        local output_file="$data_dir/${name}.tsv"

        if [[ ! -f "$output_file" ]]; then
            echo "Downloading $name dataset..."
            wget -qO- "$url" | gunzip > "$output_file"
            echo "Downloaded $name dataset to $output_file"
        else
            echo "$name dataset already exists at $output_file, skipping download."
        fi
    }

    fetch_imdb "name.basics"
    fetch_imdb "title.akas"
    fetch_imdb "title.basics"
    fetch_imdb "title.crew"
    fetch_imdb "title.episode"
    fetch_imdb "title.principals"
    fetch_imdb "title.ratings"
}

populate_imdb_data() {
    local leader_ip="$1"
    local data_dir="$SCRIPT_DIR/imdb-data"
    local psql_cmd="psql -h $leader_ip -U postgres"

    # Create database
    $psql_cmd -c "DROP DATABASE IF EXISTS imdb;"
    $psql_cmd -c "CREATE DATABASE imdb;"

    # Create schema
    $psql_cmd -d imdb -f "$data_dir/schema.sql"

    # Copy tsv files
    copy_tsv() {
        local table_name="$1"
        local file_path="$2"
        echo "Copying $table_name data from $file_path to database 'imdb' on $leader_ip"
        $psql_cmd -d imdb -c "\copy $table_name FROM '$file_path' DELIMITER E'\t' QUOTE E'\b' NULL '\N' CSV HEADER"
    }

    copy_tsv "title_akas" "$data_dir/title.akas.tsv"
    copy_tsv "title_basics" "$data_dir/title.basics.tsv"
    copy_tsv "title_crew" "$data_dir/title.crew.tsv"
    copy_tsv "title_episode" "$data_dir/title.episode.tsv"
    copy_tsv "title_principals" "$data_dir/title.principals.tsv"
    copy_tsv "title_ratings" "$data_dir/title.ratings.tsv"
    copy_tsv "name_basics" "$data_dir/name.basics.tsv"

    echo "IMDB data populated in database 'imdb' for $leader_ip"
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

    setup_postgres "pg0"
    setup_postgres "pg1"

    sudo machinectl start pg0
    sudo machinectl start pg1

    echo "Waiting for startup"
    sleep 5

    setup_replication 10.42.0.2 10.42.0.3

    download_imdb_datasets
    populate_imdb_data 10.42.0.2

    run_pgbench 10.42.0.2
fi
