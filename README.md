# Postgres Experiments

Repo where I mess around with postgres.

## TODO

Instead of imperatively creating the machines, use systemd units (machine, nspawn, networkd, link, etc). Symlink or copy them to proper locations in `/run/systemd`, and use systemd + machinectl to run the lab machines.

Consider using `arch-chroot` instead of `systemd-nspawn` to run imperative container setup commands, like `passwd -d root`
