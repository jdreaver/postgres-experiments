package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type Transaction struct {
	ID          string    `json:"id" bson:"_id"`
	Amount      string    `json:"amount" bson:"amount"`
	Currency    string    `json:"currency" bson:"currency"`
	Time        time.Time `json:"time" bson:"time"`
	Description string    `json:"description" bson:"description"`
}

func NewTransaction(i int) Transaction {
	return Transaction{
		ID:          fmt.Sprintf("tx_%d", i),
		Amount:      fmt.Sprintf("%.2f", float64(i)*10.50),
		Currency:    "USD",
		Time:        time.Now(),
		Description: fmt.Sprintf("Test transaction %d", i),
	}
}

type Config struct {
	PostgresURL string
	MongoURL    string
	Iterations  int
	Database    string
}

func main() {
	var config Config
	flag.StringVar(&config.PostgresURL, "postgres", "postgres://postgres:postgres@haproxy0:5432/postgres", "PostgreSQL connection string")
	flag.StringVar(&config.MongoURL, "mongo", "mongodb://mongo0,mongo1,mongo2/", "MongoDB connection string")
	flag.IntVar(&config.Iterations, "iterations", 10000, "Number of benchmark iterations")
	flag.StringVar(&config.Database, "database", "benchmarks", "Database name to use")
	flag.Parse()

	ctx := context.Background()

	fmt.Printf("Starting benchmark with %d iterations\n", config.Iterations)

	fmt.Println("Running PostgreSQL benchmark...")
	if err := runPostgresBenchmark(ctx, config); err != nil {
		log.Fatalf("PostgreSQL benchmark failed: %v", err)
	}

	fmt.Println("Running MongoDB benchmark...")
	if err := runMongoBenchmark(ctx, config); err != nil {
		log.Fatalf("MongoDB benchmark failed: %v", err)
	}

	fmt.Println("Benchmarks completed successfully")
}

func runPostgresBenchmark(ctx context.Context, config Config) error {
	pgConfig, err := pgx.ParseConfig(config.PostgresURL)
	if err != nil {
		return fmt.Errorf("failed to parse PostgreSQL URL: %w", err)
	}

	// N.B. Use QueryExecModeExec because the default uses statement
	// caching, which doesn't work with pgbouncer.
	pgConfig.DefaultQueryExecMode = pgx.QueryExecModeExec

	conn, err := pgx.ConnectConfig(ctx, pgConfig)
	if err != nil {
		return fmt.Errorf("failed to connect to PostgreSQL: %w", err)
	}
	defer conn.Close(ctx)

	if err := setupPostgresTable(ctx, conn); err != nil {
		return fmt.Errorf("failed to setup PostgreSQL table: %w", err)
	}

	start := time.Now()
	for i := range config.Iterations {
		tx := NewTransaction(i)

		if err := insertPostgresTransaction(ctx, conn, tx); err != nil {
			return fmt.Errorf("failed to insert transaction %d: %w", i, err)
		}

		if _, err := readPostgresTransaction(ctx, conn, tx.ID); err != nil {
			return fmt.Errorf("failed to read transaction %d: %w", i, err)
		}
	}
	duration := time.Since(start)

	fmt.Printf("PostgreSQL: %d operations in %v (%.2f ops/sec)\n",
		config.Iterations*2, duration, float64(config.Iterations*2)/duration.Seconds())

	return nil
}

func runMongoBenchmark(ctx context.Context, config Config) error {
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(config.MongoURL))
	if err != nil {
		return fmt.Errorf("failed to connect to MongoDB: %w", err)
	}
	defer client.Disconnect(ctx)

	collection := client.Database(config.Database).Collection("transactions")

	if err := setupMongoCollection(ctx, collection); err != nil {
		return fmt.Errorf("failed to setup MongoDB collection: %w", err)
	}

	start := time.Now()
	for i := range config.Iterations {
		tx := NewTransaction(i)

		if err := insertMongoTransaction(ctx, collection, tx); err != nil {
			return fmt.Errorf("failed to insert transaction %d: %w", i, err)
		}

		if _, err := readMongoTransaction(ctx, collection, tx.ID); err != nil {
			return fmt.Errorf("failed to read transaction %d: %w", i, err)
		}
	}
	duration := time.Since(start)

	fmt.Printf("MongoDB: %d operations in %v (%.2f ops/sec)\n",
		config.Iterations*2, duration, float64(config.Iterations*2)/duration.Seconds())

	return nil
}

func setupPostgresTable(ctx context.Context, conn *pgx.Conn) error {
	query := `
CREATE TABLE IF NOT EXISTS transactions (
  id TEXT PRIMARY KEY,
  amount TEXT NOT NULL,
  currency TEXT NOT NULL,
  time TIMESTAMP WITH TIME ZONE NOT NULL,
  description TEXT NOT NULL
)`

	if _, err := conn.Exec(ctx, query); err != nil {
		return err
	}

	_, err := conn.Exec(ctx, "TRUNCATE TABLE transactions")
	return err
}

func setupMongoCollection(ctx context.Context, collection *mongo.Collection) error {
	return collection.Drop(ctx)
}

func insertPostgresTransaction(ctx context.Context, conn *pgx.Conn, tx Transaction) error {
	query := `INSERT INTO transactions (id, amount, currency, time, description) VALUES ($1, $2, $3, $4, $5)`
	_, err := conn.Exec(ctx, query, tx.ID, tx.Amount, tx.Currency, tx.Time, tx.Description)
	return err
}

func readPostgresTransaction(ctx context.Context, conn *pgx.Conn, id string) (*Transaction, error) {
	var tx Transaction
	query := `SELECT id, amount, currency, time, description FROM transactions WHERE id = $1`
	err := conn.QueryRow(ctx, query, id).Scan(&tx.ID, &tx.Amount, &tx.Currency, &tx.Time, &tx.Description)
	if err != nil {
		return nil, err
	}
	return &tx, nil
}

func insertMongoTransaction(ctx context.Context, collection *mongo.Collection, tx Transaction) error {
	_, err := collection.InsertOne(ctx, tx)
	return err
}

func readMongoTransaction(ctx context.Context, collection *mongo.Collection, id string) (*Transaction, error) {
	var tx Transaction
	err := collection.FindOne(ctx, bson.M{"_id": id}).Decode(&tx)
	if err != nil {
		return nil, err
	}
	return &tx, nil
}
