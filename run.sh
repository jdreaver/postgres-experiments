#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR=$(dirname "${BASH_SOURCE[0]}")

for f in "$SCRIPT_DIR/pglab/"*.sh; do
    source "$f"
done

# CLI entrypoint if run directly
if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
    set -euo pipefail

    if [[ $# -ne 0 ]]; then
        "$@"
        exit
    fi

    setup_lab_network
    create_pgbase_machine
    build_pgdaemon

    create_machine "etcd0"
    setup_etcd "etcd0"
    sudo machinectl start etcd0

    create_machine "haproxy0"
    setup_haproxy "haproxy0"
    sudo machinectl start haproxy0

    create_machine "pg0"
    setup_postgres "pg0"
    sudo machinectl start pg0

    create_machine "pg1"
    setup_postgres "pg1"
    sudo machinectl start pg1

    create_machine "pg2"
    setup_postgres "pg2"
    sudo machinectl start pg2

    initialize_cluster_state

    echo "Waiting for startup"
    sleep 10

    download_imdb_datasets
    populate_imdb_data pg0

    run_pgbench pg0
fi
