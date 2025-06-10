package main

import (
	"context"
	"log"
	"net/http"
	"time"
)

func runHealthCheckServer(ctx context.Context, conf config) error {
	mux := http.NewServeMux()

	mux.HandleFunc("/health", healthCheck(conf, false))
	mux.HandleFunc("/primary", healthCheck(conf, true))

	srv := &http.Server{
		Addr:    conf.listenAddress,
		Handler: mux,
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

func healthCheck(conf config, checkPrimary bool) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		timeout := 500 * time.Millisecond
		// N.B. Check health through pgbouncer to ensure that is working
		isPrimary, err := checkIsPrimary(conf.pgBouncerHost, conf.pgBouncerPort, conf.postgresUser, timeout)

		status := http.StatusOK
		if err != nil {
			log.Printf("/health failed: %v", err)
			status = http.StatusServiceUnavailable
		}

		if checkPrimary && !isPrimary {
			status = http.StatusServiceUnavailable
		}

		w.WriteHeader(status)
	}
}
