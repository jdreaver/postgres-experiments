#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR=$(dirname "${BASH_SOURCE[0]}")

for f in "$SCRIPT_DIR/pglab/"*.sh; do
    source "$f"
done

# CLI entrypoint if run directly
if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
    set -euo pipefail

    LABEL=""
    if [[ -n "$TARGET" ]]; then
        LABEL="[$TARGET] "
    fi

    TARGET=${TARGET:-"unknown_target"}

    CYAN=$(tput setaf 6)
    GREEN=$(tput setaf 2)
    RED=$(tput setaf 1)
    BOLD=$(tput bold)
    NC=$(tput sgr0)

    echo -e "${BOLD}${CYAN}=== ${LABEL}running: $*${NC}"

    # Define ERR trap before the command so we print the failing module
    # in case there is an error.
    trap 'status=$?; echo -e "${BOLD}${RED}=== ${LABEL}FAILED${NC}" >&2; exit $status' ERR

    # Run command. Note we do this outside of an if statement so that
    # set -e works. sed is to prefix output
    "$@" 2>&1 | sed "s#^#${CYAN}${LABEL}${NC}#"

    echo -e "${BOLD}${GREEN}=== ${LABEL}SUCCESS${NC}"
fi
