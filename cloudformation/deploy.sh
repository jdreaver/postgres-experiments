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

upload_userdata() {
    echo "Uploading common setup and postgres setup scripts to S3 bucket: $BUCKET_NAME"
    aws s3 cp common-setup.sh "s3://$BUCKET_NAME/"
    aws s3 cp postgres-setup.sh "s3://$BUCKET_NAME/"
}

./00-deploy-s3-bucket.sh
upload_pgdaemon
upload_userdata
./10-deploy-postgres-lab.sh

# Useful commands:
#
# Cycle the ASG:
#   aws autoscaling start-instance-refresh --auto-scaling-group-name $USER-postgres-asg
#
# Nuke the DDB table:
#   aws dynamodb delete-table --table-name pgdaemon-clusters
