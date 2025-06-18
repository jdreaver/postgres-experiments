package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/google/uuid"
)

type DynamoDBBackend struct {
	client      *dynamodb.Client
	clusterName string
	nodeName    string
}

const tableName = "pgdaemon-clusters"

func NewDynamoDBBackend(client *dynamodb.Client, clusterName string, nodeName string) *DynamoDBBackend {
	return &DynamoDBBackend{
		clusterName: clusterName,
		client:      client,
		nodeName:    nodeName,
	}
}

// TODO: Table should probably be created out-of-band, not on startup?
func (d *DynamoDBBackend) InitTable(ctx context.Context) error {
	_, err := d.client.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName: aws.String(tableName),
		KeySchema: []types.KeySchemaElement{
			{
				AttributeName: aws.String("cluster_name"),
				KeyType:       types.KeyTypeHash,
			},
			{
				AttributeName: aws.String("key"),
				KeyType:       types.KeyTypeRange,
			},
		},
		AttributeDefinitions: []types.AttributeDefinition{
			{
				AttributeName: aws.String("cluster_name"),
				AttributeType: types.ScalarAttributeTypeS,
			},
			{
				AttributeName: aws.String("key"),
				AttributeType: types.ScalarAttributeTypeS,
			},
		},
		BillingMode: types.BillingModePayPerRequest,
	})
	if err != nil {
		var resourceInUse *types.ResourceInUseException
		if errors.As(err, &resourceInUse) {
			log.Printf("Table %s already exists, skipping creation", tableName)
			return nil
		}
		return fmt.Errorf("failed to create DynamoDB table: %w", err)
	}

	return nil
}

const clusterSpecRangeKey = "spec"
const clusterStatusRangeKey = "status"
const nodeStatusesRangeKey = "node-statuses"

func nodeStatusRangeKey(nodeName string) string {
	return nodeStatusesRangeKey + "/" + nodeName
}

func (d *DynamoDBBackend) AtomicWriteClusterStatus(ctx context.Context, prevStatusUUID uuid.UUID, status ClusterStatus) error {
	value, err := attributevalue.MarshalMap(status)
	if err != nil {
		return fmt.Errorf("failed to marshal cluster status: %w", err)
	}
	value["cluster_name"] = &types.AttributeValueMemberS{Value: d.clusterName}
	value["key"] = &types.AttributeValueMemberS{Value: clusterStatusRangeKey}

	putItemInput := dynamodb.PutItemInput{
		TableName: aws.String(tableName),
		Item:      value,
	}
	if prevStatusUUID != uuid.Nil {
		prevUuidAttr, err := attributevalue.Marshal(prevStatusUUID)
		if err != nil {
			return fmt.Errorf("failed to marshal previous status UUID: %w", err)
		}

		putItemInput.ConditionExpression = aws.String("status_uuid = :prev_uuid")
		putItemInput.ExpressionAttributeValues = map[string]types.AttributeValue{
			":prev_uuid": prevUuidAttr,
		}
	} else {
		putItemInput.ConditionExpression = aws.String("attribute_not_exists(status_uuid)")
	}

	if _, err := d.client.PutItem(ctx, &putItemInput); err != nil {
		var conditionErr *types.ConditionalCheckFailedException
		if errors.As(err, &conditionErr) {
			log.Printf("Cluster status write condition failed, previous UUID may not match")
			return nil
		}
		return fmt.Errorf("failed to write cluster status: %w", err)
	}

	return nil
}

func (d *DynamoDBBackend) WriteCurrentNodeStatus(ctx context.Context, status *NodeStatus) error {
	value, err := attributevalue.MarshalMap(*status)
	if err != nil {
		return fmt.Errorf("failed to marshal node status: %w", err)
	}
	value["cluster_name"] = &types.AttributeValueMemberS{Value: d.clusterName}
	value["key"] = &types.AttributeValueMemberS{Value: nodeStatusRangeKey(d.nodeName)}

	putItemInput := dynamodb.PutItemInput{
		TableName: aws.String(tableName),
		Item:      value,
	}

	if _, err := d.client.PutItem(ctx, &putItemInput); err != nil {
		return fmt.Errorf("failed to write node status to DynamoDB: %w", err)
	}

	return nil
}

func (d *DynamoDBBackend) SetClusterSpec(ctx context.Context, spec *ClusterSpec) error {
	value, err := attributevalue.MarshalMap(*spec)
	if err != nil {
		return fmt.Errorf("failed to marshal cluster spec: %w", err)
	}
	value["cluster_name"] = &types.AttributeValueMemberS{Value: d.clusterName}
	value["key"] = &types.AttributeValueMemberS{Value: clusterSpecRangeKey}

	putItemInput := dynamodb.PutItemInput{
		TableName: aws.String(tableName),
		Item:      value,
	}
	if _, err := d.client.PutItem(ctx, &putItemInput); err != nil {
		return fmt.Errorf("failed to write cluster spec to DynamoDB: %w", err)
	}

	return nil
}

func (d *DynamoDBBackend) FetchClusterState(ctx context.Context) (ClusterState, error) {
	resp, err := d.client.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(tableName),
		KeyConditionExpression: aws.String("cluster_name = :cluster_name"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":cluster_name": &types.AttributeValueMemberS{Value: d.clusterName},
		},
	})
	if err != nil {
		return ClusterState{}, fmt.Errorf("failed to query cluster state from DynamoDB: %w", err)
	}

	if len(resp.Items) == 0 {
		return ClusterState{}, fmt.Errorf("cluster state not found for cluster %s", d.clusterName)
	}

	var state ClusterState

	for _, item := range resp.Items {
		key, ok := item["key"]
		if !ok {
			return ClusterState{}, fmt.Errorf("missing key in DynamoDB item: %v", item)
		}

		var keyStr string
		if err := attributevalue.Unmarshal(key, &keyStr); err != nil {
			return ClusterState{}, fmt.Errorf("failed to unmarshal key: %w", err)
		}

		if keyStr == clusterSpecRangeKey {
			if err := attributevalue.UnmarshalMap(item, &state.Spec); err != nil {
				return ClusterState{}, fmt.Errorf("failed to unmarshal cluster spec: %w", err)
			}
		}
		if keyStr == clusterStatusRangeKey {
			if err := attributevalue.UnmarshalMap(item, &state.Status); err != nil {
				return ClusterState{}, fmt.Errorf("failed to unmarshal cluster status: %w", err)
			}
		}
		if strings.HasPrefix(keyStr, nodeStatusesRangeKey) {
			nodeName := strings.TrimPrefix(keyStr, nodeStatusesRangeKey+"/")
			var nodeStatus NodeStatus
			if err := attributevalue.UnmarshalMap(item, &nodeStatus); err != nil {
				return ClusterState{}, fmt.Errorf("failed to unmarshal node status for %s: %w", nodeName, err)
			}
			if nodeName != nodeStatus.Name {
				return ClusterState{}, fmt.Errorf("node status name mismatch: expected %s, got %s", nodeName, nodeStatus.Name)
			}
			state.Nodes = append(state.Nodes, nodeStatus)
		}
	}

	return state, nil
}
