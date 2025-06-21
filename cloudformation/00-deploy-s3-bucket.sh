#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR=$(dirname "${BASH_SOURCE[0]}")
cd "$SCRIPT_DIR"

STACK_NAME="davidreaver-postgres-lab-s3"
TEMPLATE_FILE="00-s3-bucket.yaml"
REGION="us-west-2"

echo "Deploying CloudFormation stack: $STACK_NAME"
aws cloudformation deploy \
    --template-file $TEMPLATE_FILE \
    --stack-name $STACK_NAME \
    --parameter-overrides \
        User="$USER" \
    --capabilities CAPABILITY_NAMED_IAM \
    --region $REGION

echo "Stack resources:"
aws cloudformation describe-stack-resources \
    --stack-name $STACK_NAME \
    --query "StackResources[*].[LogicalResourceId,PhysicalResourceId]" \
    --output table \
    --region $REGION

echo "Deployment complete!"
