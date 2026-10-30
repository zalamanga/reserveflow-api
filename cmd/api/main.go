package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"reserveflow/internal/api"
)

func digest(name string) ([32]byte, error) {
	var d [32]byte
	b, e := hex.DecodeString(os.Getenv(name))
	if e != nil || len(b) != 32 {
		return d, fmt.Errorf("%s must contain a SHA-256 hex digest", name)
	}
	copy(d[:], b)
	return d, nil
}
func main() {
	if len(os.Args) == 2 && os.Args[1] == "health" {
		client := http.Client{Timeout: 3 * time.Second}
		res, e := client.Get("http://127.0.0.1:8080/healthz")
		if e != nil {
			os.Exit(1)
		}
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
		if res.StatusCode != 200 {
			os.Exit(1)
		}
		return
	}
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
func run() error {
	a, err := digest("ALPHA_KEY_SHA256")
	if err != nil {
		return err
	}
	b, err := digest("BETA_KEY_SHA256")
	if err != nil {
		return err
	}
	if a == b {
		return fmt.Errorf("tenant key digests must differ")
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("DATABASE_URL is invalid")
	}
	cfg.MaxConns = 16
	cfg.MinConns = 1
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return fmt.Errorf("database configuration failed")
	}
	defer pool.Close()
	if err = pool.Ping(ctx); err != nil {
		return fmt.Errorf("database is unavailable")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("schema transaction failed")
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(81294521)`); err != nil {
		return fmt.Errorf("schema lock failed")
	}
	if _, err = tx.Exec(ctx, api.Schema); err != nil {
		return fmt.Errorf("schema initialization failed")
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("schema commit failed")
	}
	srv := &http.Server{Addr: ":8080", Handler: api.New(pool, a, b).Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}
	stopped := make(chan os.Signal, 1)
	signal.Notify(stopped, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(stopped)
	go func() {
		<-stopped
		shutdown, c := context.WithTimeout(context.Background(), 10*time.Second)
		defer c()
		srv.Shutdown(shutdown)
	}()
	log.Print("ReserveFlow listening on :8080")
	if err = srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("HTTP server failed")
	}
	return nil
}
