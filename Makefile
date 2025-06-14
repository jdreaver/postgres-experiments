MACHINES =

POSTGRES_MACHINES = pg0 pg1 pg2
MACHINES += $(POSTGRES_MACHINES)

ETCD_MACHINES = etcd0
MACHINES += $(ETCD_MACHINES)

HAPROXY_MACHINES = haproxy0
MACHINES += $(HAPROXY_MACHINES)

MONGO_MACHINES = mongo0 mongo1 mongo2
MACHINES += $(MONGO_MACHINES)

RUN=./run.sh

# Pattern rule for all machines
%: export TARGET=$@

.PHONY: all
all: machines imdb pgbench

.DEFAULT_GOAL := machines
.PHONY: machines
machines: network $(MACHINES) pg_cluster init_replset

.PHONY: network
network:
	$(RUN) setup_lab_network

.PHONY: pgbase
pgbase:
	$(RUN) create_pgbase_machine

.PHONY: pgdaemon
pgdaemon: pgbase
	$(RUN) build_pgdaemon

.PHONY: $(MACHINES)
$(MACHINES): network pgbase pgdaemon
	$(RUN) create_machine $@
	$(RUN) sudo machinectl start $@

.PHONY: init_cluster
init_cluster: $(ETCD_MACHINES)
	$(RUN) initialize_cluster_state

.PHONY: pg_cluster
pg_cluster: init_cluster $(POSTGRES_MACHINES) $(ETCD_MACHINES) $(HAPROXY_MACHINES)

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

.PHONY: test
test:
	go -C pgdaemon test ./...
