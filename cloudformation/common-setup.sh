#!/usr/bin/env bash

set -euo pipefail

# SSH public keys
# TODO: Add another public key
echo '' >> /home/ubuntu/.ssh/authorized_keys
chown ubuntu:ubuntu /home/ubuntu/.ssh/authorized_keys
