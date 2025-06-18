#!/usr/bin/env bash

create_pgbase_machine() {
    pacstrap_args=(
        -c # Use package cache on host
        -K # Do not use the host's pacman keyring
    )

    packages=(
        base
        postgresql
        pgbouncer
        openssh
        haproxy
        jre-openjdk # For DynamoDB
        aws-cli # For DynamoDB

        # Misc tools/utils
        bat
        dnsutils
        eza
        fish
        inetutils
        jq
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

    mkdir -p "$CACHE_DIR"

    download_etcd
    sudo cp "$CACHE_DIR"/etcd-bin/{etcd,etcdctl,etcdutl} "$directory/usr/bin/"

    download_dynamodb
    sudo mkdir -p "$directory/opt"
    sudo cp -r "$CACHE_DIR"/dynamodb-local "$directory/opt/dynamodb-local"

    download_mongod
    sudo cp "$CACHE_DIR"/mongod-bin/bin/mongod "$directory/usr/bin/"

    download_mongosh
    sudo cp "$CACHE_DIR"/mongosh-bin/bin/mongosh "$directory/usr/bin/"

    # Populate /etc/hosts from HOST_IPS
    for host in "${HOSTS[@]}"; do
        echo "${HOST_IPS[$host]} $host" | sudo tee -a "$directory/etc/hosts"
    done

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

download_etcd() {
    # Download etcd from https://github.com/etcd-io/etcd/releases/
    local etcd_version=v3.6.1
    local etcd_filename="etcd-${etcd_version}-linux-amd64.tar.gz"
    local dest_file="$CACHE_DIR/$etcd_filename"

    if [[ ! -f "$dest_file" ]]; then
        echo "Downloading etcd $etcd_version to $dest_file..."
        local etcd_url="https://github.com/etcd-io/etcd/releases/download/${etcd_version}/$etcd_filename"
        curl -L "$etcd_url" -o "$dest_file"
        rm -rf "$CACHE_DIR/etcd-bin"
        mkdir -p "$CACHE_DIR/etcd-bin"
        tar -xzf "$dest_file" -C "$CACHE_DIR/etcd-bin" --strip-components=1
    fi
}

download_dynamodb() {
    # https://docs.aws.amazon.com/amazondynamodb/latest/developerguide/DynamoDBLocal.DownloadingAndRunning.html
    local ddb_filename="dynamodb_local_latest.tar.gz"
    local dest_file="$CACHE_DIR/$ddb_filename"

    if [[ ! -f "$dest_file" ]]; then
        echo "Downloading DynamoDB to $dest_file..."
        local ddb_url="https://d1ni2b6xgvw0s0.cloudfront.net/v2.x/$ddb_filename"
        curl -L "$ddb_url" -o "$dest_file"
        rm -rf "$CACHE_DIR/dynamodb-local"
        mkdir -p "$CACHE_DIR/dynamodb-local"
        tar -xzf "$dest_file" -C "$CACHE_DIR/dynamodb-local"
    fi
}

download_mongod() {
    # Download mongodb with URL from
    # https://www.mongodb.com/try/download/community
    local mongo_filename="mongodb-linux-x86_64-ubuntu2404-8.0.10.tgz"
    local dest_file="$CACHE_DIR/$mongo_filename"

    if [[ ! -f "$dest_file" ]]; then
        echo "Downloading $dest_file..."
        local mongo_url="https://fastdl.mongodb.org/linux/$mongo_filename"
        curl -L "$mongo_url" -o "$dest_file"
        rm -rf "$CACHE_DIR/mongod-bin"
        mkdir -p "$CACHE_DIR/mongod-bin"
        tar -xzf "$dest_file" -C "$CACHE_DIR/mongod-bin" --strip-components=1
    fi
}

download_mongosh() {
    # Download mongosh from https://www.mongodb.com/try/download/shell
    local mongosh_filename=mongosh-2.5.2-linux-x64.tgz
    local dest_file="$CACHE_DIR/$mongosh_filename"

    if [[ ! -f "$dest_file" ]]; then
        local mongosh_url="https://downloads.mongodb.com/compass/$mongosh_filename"
        curl -L "$mongosh_url" -o "$dest_file"
        rm -rf "$CACHE_DIR/mongosh-bin"
        mkdir -p "$CACHE_DIR/mongosh-bin"
        tar -xzf "$dest_file" -C "$CACHE_DIR/mongosh-bin" --strip-components=1
    fi
}

create_machine() {
    if [[ $# -ne 1 ]]; then
        echo "Usage: create_machine <name>"
        return 1
    fi

    local name="$1"

    create_machine_basics "$name"

    case "$name" in
        etcd*)
            setup_etcd "$name"
            ;;
        haproxy*)
            setup_haproxy "$name"
            ;;
        pg*)
            setup_postgres "$name"
            ;;
        mongo*)
            setup_mongo "$name"
            ;;
        dynamodb*)
            setup_dynamodb "$name"
            ;;
        *)
            echo "ERROR: Unknown machine name '$name'."
            return 1
            ;;
    esac
}


create_machine_basics() {
    if [[ $# -ne 1 ]]; then
        echo "Usage: create_machine_basics <name>"
        return 1
    fi

    local name="$1"

    # Stop machine if it is running
    if sudo machinectl status "$name" &>/dev/null; then
        echo "Stopping existing machine '$name'..."
        sudo machinectl terminate "$name" || true

        echo "Waiting for machine '$name' to fully stop..."
        timeout=10
        while sudo machinectl status "$name" &>/dev/null; do
            sleep 0.2
            ((timeout--))
            if ((timeout == 0)); then
                echo "Machine did not stop in time, sending SIGKILL..."
                sudo machinectl kill "$name" --signal=SIGKILL || true
                sleep 0.2
            fi
        done

        # Sometimes this mountpoint still exists, so force unmount
        mountpoint="/run/systemd/nspawn/unix-export/$name"
        sudo umount -lf "$mountpoint" || true
    fi

    local directory="/var/lib/machines/$name"

    sudo rm -rf "$directory"
    sudo cp --archive /var/lib/machines/pgbase "$directory"

    # N.B. Network file must start with 10- to be loaded before
    # /usr/lib/systemd/network/80-container-host0.network
    sudo tee "$directory/etc/systemd/network/10-host0.network" > /dev/null <<EOF
[Match]
Name=host0

[Network]
Address=${HOST_IPS[$name]}/$IP_CIDR_SLASH
Gateway=${HOST_IPS[host]}
DNS=${HOST_IPS[host]}
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
