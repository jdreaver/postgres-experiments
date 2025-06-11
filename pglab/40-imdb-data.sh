#!/usr/bin/env bash

download_imdb_datasets() {
    local data_dir="$SCRIPT_DIR/imdb-data"
    mkdir -p "$data_dir"

    fetch_imdb() {
        local name="$1"
        local url="https://datasets.imdbws.com/${name}.tsv.gz"
        local output_file="$data_dir/${name}.tsv"

        if [[ ! -f "$output_file" ]]; then
            echo "Downloading $name dataset..."
            wget -qO- "$url" | gunzip > "$output_file"
            echo "Downloaded $name dataset to $output_file"
        else
            echo "$name dataset already exists at $output_file, skipping download."
        fi
    }

    fetch_imdb "name.basics"
    fetch_imdb "title.akas"
    fetch_imdb "title.basics"
    fetch_imdb "title.crew"
    fetch_imdb "title.episode"
    fetch_imdb "title.principals"
    fetch_imdb "title.ratings"
}

populate_imdb_data() {
    local leader="$1"

    local leader_ip="${HOST_IPS[$leader]}"
    local data_dir="$SCRIPT_DIR/imdb-data"
    local psql_cmd="psql -h $leader_ip -U postgres"

    # Create database
    $psql_cmd -c "DROP DATABASE IF EXISTS imdb;"
    $psql_cmd -c "CREATE DATABASE imdb;"

    # Create schema
    $psql_cmd -d imdb -f "$data_dir/schema.sql"

    # Copy tsv files
    copy_tsv() {
        local table_name="$1"
        local file_path="$2"
        echo "Copying $table_name data from $file_path to database 'imdb' on $leader ($leader_ip)..."
        $psql_cmd -d imdb -c "\copy $table_name FROM '$file_path' DELIMITER E'\t' QUOTE E'\b' NULL '\N' CSV HEADER"
    }

    copy_tsv "title_akas" "$data_dir/title.akas.tsv"
    copy_tsv "title_basics" "$data_dir/title.basics.tsv"
    copy_tsv "title_crew" "$data_dir/title.crew.tsv"
    copy_tsv "title_episode" "$data_dir/title.episode.tsv"
    copy_tsv "title_principals" "$data_dir/title.principals.tsv"
    copy_tsv "title_ratings" "$data_dir/title.ratings.tsv"
    copy_tsv "name_basics" "$data_dir/name.basics.tsv"

    echo "IMDB data populated in database 'imdb' for $leader_ip"
}
