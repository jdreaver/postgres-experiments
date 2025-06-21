#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR=$(dirname "${BASH_SOURCE[0]}")
cd "$SCRIPT_DIR"

STACK_NAME="davidreaver-postgres-lab"
TEMPLATE_FILE="10-postgres-lab.yaml"
REGION="us-west-2"
VPC_NAME="qa northwest"
SUBNET_NAMES="qa internal us-west-2a,qa internal us-west-2b,qa internal us-west-2c"
UBUNTU_VERSION="24.04"

echo "Fetching current public IP"
PUBLIC_IP=$(curl -s https://api.ipify.org)
echo "Current public IP: $PUBLIC_IP"

echo "Fetching VPC ID for VPC name: $VPC_NAME"
VPC_ID=$(aws ec2 describe-vpcs \
    --filters "Name=tag:Name,Values=$VPC_NAME" \
    --query "Vpcs[0].VpcId" \
    --output text \
    --region $REGION)

if [ -z "$VPC_ID" ]; then
    echo "Error: VPC with name '$VPC_NAME' not found"
    exit 1
fi
echo "Found VPC ID: $VPC_ID"

echo "Fetching Subnet IDs for subnet names: '$SUBNET_NAMES' in VPC: $VPC_ID"
SUBNET_IDS=$(aws ec2 describe-subnets \
    --filters "Name=vpc-id,Values=$VPC_ID" "Name=tag:Name,Values=$SUBNET_NAMES" \
    --query "Subnets[*].SubnetId" \
    --output text \
    --region $REGION)

if [ -z "$SUBNET_IDS" ]; then
    echo "Error: Subnets with name '$SUBNET_NAMES' not found in VPC '$VPC_ID'"
    exit 1
fi
echo "Found Subnet IDs: $SUBNET_IDS"

# Convert newline/tab-separated subnet IDs to comma-separated
SUBNET_IDS=$(echo "$SUBNET_IDS" | tr '\n\t' ',' | sed 's/,$//')

echo "Searching for Ubuntu $UBUNTU_VERSION AMI in region $REGION"
aws ec2 describe-images \
    --owners 099720109477 \
    --filters "Name=name,Values=ubuntu/images/hvm-ssd-gp3/ubuntu-*-$UBUNTU_VERSION-amd64-server-*" \
    --query 'Images[*].[ImageId,Name,CreationDate]' \
    --output table \
    --region $REGION

echo "Fetching latest Ubuntu $UBUNTU_VERSION AMI ID in region $REGION"
AMI_ID=$(aws ec2 describe-images \
    --owners 099720109477 \
    --filters "Name=name,Values=ubuntu/images/hvm-ssd-gp3/ubuntu-*-$UBUNTU_VERSION-amd64-server-*" \
    --query "sort_by(Images, &CreationDate)[-1].ImageId" \
    --output text \
    --region $REGION)

if [ -z "$AMI_ID" ] || [ "$AMI_ID" == "None" ]; then
    echo "Error: Ubuntu $UBUNTU_VERSION AMI not found"
    exit 1
fi
echo "Found AMI ID: $AMI_ID"

echo "Deploying CloudFormation stack: $STACK_NAME"
aws cloudformation deploy \
    --template-file $TEMPLATE_FILE \
    --stack-name $STACK_NAME \
    --parameter-overrides \
        User="$USER" \
        PublicIP="$PUBLIC_IP" \
        VpcId="$VPC_ID" \
        SubnetIds="$SUBNET_IDS" \
        UbuntuAmiId="$AMI_ID" \
    --capabilities CAPABILITY_NAMED_IAM \
    --region $REGION

echo "Stack resources:"
aws cloudformation describe-stack-resources \
    --stack-name $STACK_NAME \
    --query "StackResources[*].[LogicalResourceId,PhysicalResourceId]" \
    --output table \
    --region $REGION

echo "Deployment complete!"
