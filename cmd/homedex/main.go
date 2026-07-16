package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/HarshShah0203/homedex/internal/auth"
	"github.com/HarshShah0203/homedex/internal/server"
	"github.com/HarshShah0203/homedex/internal/store"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		slog.Error("homedex stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
	dataDefault := envString("HOMEDEX_DATA_DIR", "data")
	listenDefault := envString("HOMEDEX_LISTEN", ":7377")
	dataDir := flag.String("data-dir", dataDefault, "directory for the SQLite database and instance key")
	listen := flag.String("listen", listenDefault, "HTTP listen address")
	flag.Parse()
	if err := os.MkdirAll(*dataDir, 0700); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	// Initialize the encryption key at startup even before connector CRUD lands,
	// so every installation has secure-at-rest primitives from its first boot.
	if _, err := auth.LoadOrCreateSecretBox(*dataDir); err != nil {
		return fmt.Errorf("initialize instance key: %w", err)
	}
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(*dataDir, "homedex.db"))
	if err != nil {
		return err
	}
	defer st.Close()
	broker := server.NewBroker()
	handler := server.New(st, broker, server.Config{Version: version, NoAuth: envBool("HOMEDEX_NO_AUTH", false), SecureCookies: envBool("HOMEDEX_SECURE_COOKIES", false)})
	httpServer := &http.Server{Addr: *listen, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 0, IdleTimeout: 60 * time.Second}
	errCh := make(chan error, 1)
	go func() {
		slog.Info("homedex listening", "address", *listen, "version", version)
		errCh <- httpServer.ListenAndServe()
	}()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	select {
	case sig := <-sigCh:
		slog.Info("shutting down", "signal", sig)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
func envString(key, def string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return def
}
func envBool(key string, def bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return def
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return def
	}
	return parsed
}
