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

    # Download etcd from https://github.com/etcd-io/etcd/releases/
    local etcd_version=v3.6.1
    local filename="etcd-${etcd_version}-linux-amd64.tar.gz"
    local etcd_url="https://github.com/etcd-io/etcd/releases/download/${etcd_version}/$filename"
    curl -L "$etcd_url" -o "/tmp/$filename"
    sudo tar -xzf "/tmp/$filename" -C "$directory/usr/bin" --strip-components=1

    # Populate /etc/hosts from HOST_IPS
    for host in "${HOSTS[@]}"; do
        echo "${HOST_IPS[$host]} $host" | sudo tee -a "$directory/etc/hosts"
    done

    # Allow postgres user to start and stop postgres
    sudo tee "$directory/etc/sudoers.d/100-postgres" > /dev/null <<EOF
postgres ALL=(ALL) NOPASSWD: /usr/bin/systemctl start postgresql.service, /usr/bin/systemctl stop postgresql.service, /usr/bin/systemctl start pgbouncer.service, /usr/bin/systemctl stop pgbouncer.service
EOF

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
        sudo machinectl stop "$name"
        while sudo machinectl status "$name" &>/dev/null; do
            echo "Waiting for machine '$name' to stop..."
            sleep 1
            sudo machinectl kill "$name" --signal=SIGKILL &>/dev/null || true
        done
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
