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

    # Run the actual command and prefix output
    if "$@" 2>&1 | sed "s#^#${CYAN}${LABEL}${NC}#"; then
      echo -e "${BOLD}${GREEN}=== ${LABEL}SUCCESS${NC}"
    else
      echo -e "${BOLD}${RED}=== ${LABEL}FAILED${NC}"
      exit 1
    fi
fi
