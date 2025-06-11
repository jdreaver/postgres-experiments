MACHINES =  pg0 pg1 pg2
MACHINES += etcd0
MACHINES += haproxy0

MONGO_MACHINES = mongo0 mongo1 mongo2
MACHINES += $(MONGO_MACHINES)

RUN=./run.sh

# Pattern rule for all machines
%: export TARGET=$@

.PHONY: all
all: network $(MACHINES) init_cluster imdb pgbench

.DEFAULT_GOAL := machines
.PHONY: machines
machines: $(MACHINES)

.PHONY: network
network:
	$(RUN) setup_lab_network

.PHONY: pgbase
pgbase:
	$(RUN) create_pgbase_machine
	$(RUN) build_pgdaemon

.PHONY: $(MACHINES)
$(MACHINES): network pgbase
	$(RUN) create_machine $@
	$(RUN) sudo machinectl start $@

.PHONY: init_cluster
init_cluster: etcd0
	$(RUN) initialize_cluster_state

.PHONY: init_replset
init_replset: $(MONGO_MACHINES)
	$(RUN) init_mongo_replset

.PHONY: imdb
imdb: pg0 init_cluster
	$(RUN) download_imdb_datasets
	$(RUN) populate_imdb_data $<

.PHONY: pgbench
pgbench: pg0 init_cluster
	$(RUN) run_pgbench $<
