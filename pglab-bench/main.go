package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func main() {
	postgresURL := flag.String("postgres", "postgres://postgres:postgres@haproxy0:5432/postgres", "PostgreSQL connection string")
	mongoURL := flag.String("mongo", "mongodb://mongo0,mongo1,mongo2/", "MongoDB connection string")
	duration := flag.Duration("duration", 5*time.Second, "Duration to run each benchmark")
	database := flag.String("database", "benchmarks", "Database name to use")
	clients := flag.Int("clients", 1, "Number of concurrent clients")
	flag.Parse()

	ctx := context.Background()

	fmt.Printf("Starting benchmarks with %v duration and %d clients\n", *duration, *clients)

	databases := []func() (BenchmarkDatabase, error){
		func() (BenchmarkDatabase, error) {
			return NewPostgresDB(ctx, *postgresURL)
		},
		func() (BenchmarkDatabase, error) {
			return NewPostgresJsonbDB(ctx, *postgresURL)
		},
		func() (BenchmarkDatabase, error) {
			return NewMongoDB(ctx, *mongoURL, *database)
		},
	}

	for _, dbFactory := range databases {
		db, err := dbFactory()
		if err != nil {
			log.Fatalf("Failed to create database: %v", err)
		}

		fmt.Printf("Running %s benchmark...\n", db.Name())
		if err := runBenchmark(ctx, dbFactory, *duration, *clients); err != nil {
			db.Close(ctx)
			log.Fatalf("%s benchmark failed: %v", db.Name(), err)
		}

		if err := db.Close(ctx); err != nil {
			log.Printf("Warning: failed to close %s connection: %v", db.Name(), err)
		}
	}

	fmt.Println("Benchmarks completed successfully")
}

func runBenchmark(ctx context.Context, dbFactory func() (BenchmarkDatabase, error), duration time.Duration, clients int) error {
	// Setup once with a single connection
	setupDB, err := dbFactory()
	if err != nil {
		return fmt.Errorf("failed to create setup database: %w", err)
	}

	if err := setupDB.Setup(ctx); err != nil {
		return fmt.Errorf("failed to setup %s: %w", setupDB.Name(), err)
	}

	if err := setupDB.Close(ctx); err != nil {
		return fmt.Errorf("failed to close setup connection: %w", err)
	}

	// Shared state for all clients
	var totalOperations int64
	var txIDCounter int64
	deadline := time.Now().Add(duration)

	// Start all client goroutines
	var wg sync.WaitGroup
	start := time.Now()

	for range clients {
		wg.Add(1)
		go func() {
			defer wg.Done()

			clientDB, err := dbFactory()
			if err != nil {
				log.Printf("Failed to create client database connection: %v", err)
				return
			}
			defer clientDB.Close(ctx)

			clientOps := 0
			for time.Now().Before(deadline) {
				txID := int(atomic.AddInt64(&txIDCounter, 1) - 1)
				tx := NewTransaction(txID)

				if err := clientDB.InsertTransaction(ctx, tx); err != nil {
					log.Printf("Failed to insert transaction %d: %v", txID, err)
					continue
				}
				clientOps++

				if time.Now().Before(deadline) {
					if _, err := clientDB.ReadTransaction(ctx, tx.ID); err != nil {
						log.Printf("Failed to read transaction %d: %v", txID, err)
						continue
					}
					clientOps++
				}
			}
			atomic.AddInt64(&totalOperations, int64(clientOps))
		}()
	}

	wg.Wait()
	actualDuration := time.Since(start)

	fmt.Printf("%s: %d operations in %v (%.2f ops/sec)\n",
		setupDB.Name(), totalOperations, actualDuration, float64(totalOperations)/actualDuration.Seconds())

	return nil
}

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

type BenchmarkDatabase interface {
	Name() string
	Setup(ctx context.Context) error
	InsertTransaction(ctx context.Context, tx Transaction) error
	ReadTransaction(ctx context.Context, id string) (*Transaction, error)
	Close(ctx context.Context) error
}

func connectPostgres(ctx context.Context, url string) (*pgx.Conn, error) {
	pgConfig, err := pgx.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("failed to parse PostgreSQL URL: %w", err)
	}
	// N.B. Use QueryExecModeExec because the default uses statement
	// caching, which doesn't work with pgbouncer.
	pgConfig.DefaultQueryExecMode = pgx.QueryExecModeExec

	conn, err := pgx.ConnectConfig(ctx, pgConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to PostgreSQL: %w", err)
	}

	return conn, nil
}

type PostgresDB struct {
	conn *pgx.Conn
}

func NewPostgresDB(ctx context.Context, url string) (*PostgresDB, error) {
	conn, err := connectPostgres(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("Failed to create postgres connection: %v", err)
	}

	return &PostgresDB{conn: conn}, nil
}

func (p *PostgresDB) Name() string { return "PostgreSQL" }

func (p *PostgresDB) Setup(ctx context.Context) error {
	query := `
DROP TABLE IF EXISTS transactions;

CREATE TABLE IF NOT EXISTS transactions (
  id TEXT PRIMARY KEY,
  amount TEXT NOT NULL,
  currency TEXT NOT NULL,
  time TIMESTAMP WITH TIME ZONE NOT NULL,
  description TEXT NOT NULL
);`

	_, err := p.conn.Exec(ctx, query)
	return err
}

func (p *PostgresDB) InsertTransaction(ctx context.Context, tx Transaction) error {
	query := `INSERT INTO transactions (id, amount, currency, time, description) VALUES ($1, $2, $3, $4, $5)`
	_, err := p.conn.Exec(ctx, query, tx.ID, tx.Amount, tx.Currency, tx.Time, tx.Description)
	return err
}

func (p *PostgresDB) ReadTransaction(ctx context.Context, id string) (*Transaction, error) {
	var tx Transaction
	query := `SELECT id, amount, currency, time, description FROM transactions WHERE id = $1`
	err := p.conn.QueryRow(ctx, query, id).Scan(&tx.ID, &tx.Amount, &tx.Currency, &tx.Time, &tx.Description)
	if err != nil {
		return nil, err
	}
	return &tx, nil
}

func (p *PostgresDB) Close(ctx context.Context) error {
	return p.conn.Close(ctx)
}

type PostgresJsonbDB struct {
	conn *pgx.Conn
}

func NewPostgresJsonbDB(ctx context.Context, url string) (*PostgresJsonbDB, error) {
	conn, err := connectPostgres(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("Failed to create postgres connection: %v", err)
	}

	return &PostgresJsonbDB{conn: conn}, nil
}

func (p *PostgresJsonbDB) Name() string { return "PostgreSQL (jsonb)" }

func (p *PostgresJsonbDB) Setup(ctx context.Context) error {
	query := `
DROP TABLE IF EXISTS transactions_jsonb;

CREATE TABLE IF NOT EXISTS transactions_jsonb (
  id TEXT PRIMARY KEY,
  data JSONB NOT NULL
);`

	_, err := p.conn.Exec(ctx, query)
	return err
}

func (p *PostgresJsonbDB) InsertTransaction(ctx context.Context, tx Transaction) error {
	txBytes, err := json.Marshal(tx)
	if err != nil {
		return fmt.Errorf("failed to marshal transaction: %w", err)
	}

	query := `INSERT INTO transactions_jsonb (id, data) VALUES ($1, $2)`
	_, err = p.conn.Exec(ctx, query, tx.ID, string(txBytes))
	return err
}

func (p *PostgresJsonbDB) ReadTransaction(ctx context.Context, id string) (*Transaction, error) {
	var data string
	query := `SELECT data FROM transactions_jsonb WHERE id = $1`
	err := p.conn.QueryRow(ctx, query, id).Scan(&data)
	if err != nil {
		return nil, err
	}

	var tx Transaction
	if err := json.Unmarshal([]byte(data), &tx); err != nil {
		return nil, fmt.Errorf("failed to unmarshal transaction: %w", err)
	}

	return &tx, nil
}

func (p *PostgresJsonbDB) Close(ctx context.Context) error {
	return p.conn.Close(ctx)
}

type MongoDB struct {
	client     *mongo.Client
	collection *mongo.Collection
}

func NewMongoDB(ctx context.Context, url, database string) (*MongoDB, error) {
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(url))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to MongoDB: %w", err)
	}

	collection := client.Database(database).Collection("transactions")
	return &MongoDB{client: client, collection: collection}, nil
}

func (m *MongoDB) Name() string { return "MongoDB" }

func (m *MongoDB) Setup(ctx context.Context) error {
	return m.collection.Drop(ctx)
}

func (m *MongoDB) InsertTransaction(ctx context.Context, tx Transaction) error {
	_, err := m.collection.InsertOne(ctx, tx)
	return err
}

func (m *MongoDB) ReadTransaction(ctx context.Context, id string) (*Transaction, error) {
	var tx Transaction
	err := m.collection.FindOne(ctx, bson.M{"_id": id}).Decode(&tx)
	if err != nil {
		return nil, fmt.Errorf("failed to read Mongo transaction: %w", err)
	}
	return &tx, nil
}

func (m *MongoDB) Close(ctx context.Context) error {
	return m.client.Disconnect(ctx)
}
