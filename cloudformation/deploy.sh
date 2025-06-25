#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR=$(dirname "${BASH_SOURCE[0]}")
cd "$SCRIPT_DIR"

BUCKET_NAME="$USER-postgres-lab"

upload_pgdaemon() {
    echo "Building and uploading pgdaemon to S3 bucket: $BUCKET_NAME"
    GOOS=linux GOARCH=amd64 go build -C "$SCRIPT_DIR/../pgdaemon" -o "$(realpath "$SCRIPT_DIR")/pgdaemon"
    aws s3 cp pgdaemon "s3://$BUCKET_NAME/"
}

upload_pglab_bench() {
    echo "Building and uploading pglab-bench to S3 bucket: $BUCKET_NAME"
    GOOS=linux GOARCH=amd64 go build -C "$SCRIPT_DIR/../pglab-bench" -o "$(realpath "$SCRIPT_DIR")/pglab-bench"
    aws s3 cp pglab-bench "s3://$BUCKET_NAME/"
}

upload_userdata() {
    echo "Uploading common setup and postgres setup scripts to S3 bucket: $BUCKET_NAME"
    aws s3 cp common-setup.sh "s3://$BUCKET_NAME/"
    aws s3 cp postgres-setup.sh "s3://$BUCKET_NAME/"
    aws s3 cp mongodb-setup.sh "s3://$BUCKET_NAME/"
    aws s3 cp jump-box-setup.sh "s3://$BUCKET_NAME/"
}

./00-deploy-s3-bucket.sh
upload_pgdaemon
upload_pglab_bench
upload_userdata
./10-deploy-postgres-lab.sh

# Useful commands:
#
# Cycle the ASG:
#   aws autoscaling set-desired-capacity --auto-scaling-group-name $USER-pglab-postgres --desired-capacity 0
#   aws autoscaling set-desired-capacity --auto-scaling-group-name $USER-pglab-postgres --desired-capacity 3
#
# Nuke the DDB table:
#   aws dynamodb delete-table --table-name pgdaemon-clusters
