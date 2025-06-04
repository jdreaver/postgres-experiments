# Postgres Experiments

Repo where I mess around with postgres.

## TODO

Instead of imperatively creating the machines, use systemd units (machine, nspawn, networkd, link, etc) in this repo, symlink them to proper locations in `/etc/systemd/system/`, and use systemd + machinectl to run the lab machines. Symlinking into this repo is good because then I'll know where they came from if I look for them.
