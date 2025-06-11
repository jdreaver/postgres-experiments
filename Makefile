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

$(PG_MACHINES): network pgbase
	$(RUN) create_machine $@
	$(RUN) setup_postgres $@
	$(RUN) sudo machinectl start $@

$(ETCD_MACHINES): network pgbase
	$(RUN) create_machine $@
	$(RUN) setup_etcd $@
	$(RUN) sudo machinectl start $@

$(HAPROXY_MACHINES): network pgbase
	$(RUN) create_machine $@
	$(RUN) setup_haproxy $@
	$(RUN) sudo machinectl start $@

init_cluster: etcd0
	$(RUN) initialize_cluster_state

imdb: pg0 init_cluster
	@echo Waiting for pg0 to be ready...
	sleep 20 # TODO: Replace with a proper wait mechanism
	$(RUN) download_imdb_datasets
	$(RUN) populate_imdb_data $<

pgbench: pg0 init_cluster
	@echo Waiting for pg0 to be ready...
	sleep 20 # TODO: Replace with a proper wait mechanism
	$(RUN) run_pgbench $<
