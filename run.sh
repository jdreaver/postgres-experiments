#!/usr/bin/env bash

set -euo pipefail

# Create an Arch Linux rootfs for use with systemd-nspawn
create_arch_rootfs() {
    if [[ $# -ne 1 ]]; then
        echo "Usage: create_arch_rootfs <directory>"
        return 1
    fi

    local directory="$1"

    args=(
        -c # Use package cache on host
        -K # Do not use the host's pacman keyring
    )

    packages=(
        base
        postgresql
    )

    echo "Creating Arch Linux rootfs in '$directory'"

    sudo rm -rf "$directory"
    mkdir -p "$directory"

    sudo pacstrap "${args[@]}" "$directory" "${packages[@]}"

    # Don't require a password for root in the container
    sudo systemd-nspawn -D "$directory" passwd -d root
}

setup_bridge_lab0() {
    local bridge=lab0
    local bridge_ip=10.42.0.1/24

    # Create bridge if it doesn't exist
    if ! ip link show "$bridge" &>/dev/null; then
        echo "Creating bridge: $bridge"
        sudo ip link add name "$bridge" type bridge
    fi

    # Bring up the bridge
    sudo ip link set "$bridge" up

    # Assign IP if not already present
    if ! ip addr show dev "$bridge" | grep -q "$bridge_ip"; then
        sudo ip addr add "$bridge_ip" dev "$bridge"
    fi
}

setup_container_veth() {
    local name="$1"
    local bridge=lab0
    local veth_host="veth-host-$name"
    local veth_cont="veth-cont-$name"

    # Delete existing veth pair if needed (idempotency)
    if ip link show "$veth_host" &>/dev/null; then
        sudo ip link delete "$veth_host"
    fi

    # Create veth pair
    sudo ip link add "$veth_host" type veth peer name "$veth_cont"

    # Attach host end to bridge
    sudo ip link set "$veth_host" master "$bridge"
    sudo ip link set "$veth_host" up

    # Set container end up, but don't attach to anything yet (nspawn will)
    sudo ip link set "$veth_cont" up
}

start_pg_container() {
    local name="$1"
    local directory="$2"
    sudo systemd-nspawn -M "$name" -D "$directory" \
        --network-interface="veth-cont-$name" \
        --boot
}

setup_and_run_pg() {
    # Create base-image if it doesn't exist
    if [[ ! -d base-image ]]; then
        echo "Creating base image..."
        create_arch_rootfs base-image
    else
        echo "Base image already exists, skipping creation."
    fi

    setup_bridge_lab0
    setup_container_veth pg1
    start_pg_container pg1 base-image
}

# CLI entrypoint if run directly
if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
    set -euo pipefail

    # If no args, fail
    if [[ $# -eq 0 ]]; then
        echo "Usage: $0 <command> (see script source for available commands)"
        exit 1
    fi

    "$@"
fi
