ETCD_MACHINES=etcd0
PG_MACHINES=pg0 pg1 pg2
HAPROXY_MACHINES=haproxy0
MACHINES=$(ETCD_MACHINES) $(PG_MACHINES) $(HAPROXY_MACHINES)

RUN=./run.sh

# Pattern rule for all machines
%: export TARGET=$@

.PHONY: all
all: network $(MACHINES) init_cluster imdb pgbench

.DEFAULT_GOAL := machines
.PHONY: machines
machines: $(MACHINES)

network:
	$(RUN) setup_lab_network

pgbase:
	$(RUN) create_pgbase_machine
	$(RUN) build_pgdaemon

$(MACHINES): network pgbase
	$(RUN) create_machine $@
	$(RUN) sudo machinectl start $@

init_cluster: etcd0
	$(RUN) initialize_cluster_state

imdb: pg0 init_cluster
	$(RUN) download_imdb_datasets
	$(RUN) populate_imdb_data $<

pgbench: pg0 init_cluster
	$(RUN) run_pgbench $<
