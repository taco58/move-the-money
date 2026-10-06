package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"time"

	"koho-move-the-money/internal/httpapi"
	"koho-move-the-money/internal/money"
	"koho-move-the-money/internal/storage"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	db, err := storage.Open(ctx, envOrDefault("DB_PATH", "money.db"))
	cancel()
	if err != nil {
		return err
	}
	defer db.Close()

	server := &http.Server{
		Addr:              ":" + envOrDefault("PORT", "8080"),
		Handler:           httpapi.NewHandler(money.NewService(db)),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("listening on %s", server.Addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
