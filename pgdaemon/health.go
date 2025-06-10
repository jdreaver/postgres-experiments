package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"
)

func runHealthCheckServer(ctx context.Context, conf config) error {
	srv := &http.Server{
		Addr: conf.listenAddress,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			timeout := 500 * time.Millisecond
			pgOK, pgErr := checkDB(conf.postgresHost, conf.postgresPort, conf.postgresUser, timeout)
			pbOK, pbErr := checkDB(conf.pgBouncerHost, conf.pgBouncerPort, conf.postgresUser, timeout)

			resp := HealthResponse{
				PostgresOK:   pgOK,
				PostgresErr:  pgErr.Error(),
				PgBouncerOK:  pbOK,
				PgBouncerErr: pbErr.Error(),
			}

			status := http.StatusOK
			if !pgOK || !pbOK {
				status = http.StatusServiceUnavailable
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			json.NewEncoder(w).Encode(resp)
		}),
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx) // graceful shutdown
	}()

	log.Printf("Listening on %s", srv.Addr)
	return srv.ListenAndServe()
}
