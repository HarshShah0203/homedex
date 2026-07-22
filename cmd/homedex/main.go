package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/HarshShah0203/homedex/internal/auth"
	"github.com/HarshShah0203/homedex/internal/connectors"
	"github.com/HarshShah0203/homedex/internal/connectors/caddy"
	"github.com/HarshShah0203/homedex/internal/connectors/docker"
	"github.com/HarshShah0203/homedex/internal/connectors/npm"
	"github.com/HarshShah0203/homedex/internal/connectors/rdap"
	"github.com/HarshShah0203/homedex/internal/connectors/sshexec"
	"github.com/HarshShah0203/homedex/internal/connectors/tlsprobe"
	"github.com/HarshShah0203/homedex/internal/connectors/traefik"
	"github.com/HarshShah0203/homedex/internal/engine"
	"github.com/HarshShah0203/homedex/internal/notify"
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
	trustedProxies, err := server.ParseTrustedProxies(os.Getenv("HOMEDEX_TRUSTED_PROXIES"))
	if err != nil {
		return fmt.Errorf("parse HOMEDEX_TRUSTED_PROXIES: %w", err)
	}
	goneRetention, err := envDays("HOMEDEX_GONE_RETENTION_DAYS", 30)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*dataDir, 0700); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	box, err := auth.LoadOrCreateSecretBox(*dataDir)
	if err != nil {
		return fmt.Errorf("initialize instance key: %w", err)
	}
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(*dataDir, "homedex.db"))
	if err != nil {
		return err
	}
	defer st.Close()
	broker := server.NewBroker()
	registry := connectors.NewRegistry()
	for _, c := range []connectors.Connector{docker.New(), traefik.New(), caddy.New(), npm.New(), tlsprobe.New(), rdap.New(), sshexec.New()} {
		if err = registry.Register(c); err != nil {
			return err
		}
	}
	configs := store.NewConnectorConfigs(st, box)
	applier := engine.New(st, broker)
	notifications, err := notify.NewManager(ctx, st, box, notify.ShoutrrrSender{})
	if err != nil {
		return fmt.Errorf("initialize notification secrets: %w", err)
	}
	applier.SetRuleEvaluator(notifications)
	runner := engine.NewRunner(st, configs, registry, applier)
	appCtx, cancelApp := context.WithCancel(context.Background())
	defer cancelApp()
	scheduler := engine.NewScheduler(runner)
	if err = scheduler.SetGoneRetention(goneRetention); err != nil {
		return err
	}
	go scheduler.Run(appCtx)
	secureCookies := envBool("HOMEDEX_SECURE_COOKIES", false)
	handler := server.New(st, broker, server.Config{Version: version, NoAuth: envBool("HOMEDEX_NO_AUTH", false), SecureCookies: secureCookies, TrustedProxies: trustedProxies, ConnectorConfigs: configs, Registry: registry, Runner: runner, Notifications: notifications})
	httpServer := &http.Server{Addr: *listen, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 0, IdleTimeout: 60 * time.Second}
	if !secureCookies && !isLoopbackListen(*listen) {
		slog.Warn("WARNING: HOMEDEX_SECURE_COOKIES is off and Homedex is not bound to loopback; the session cookie will be sent in cleartext. Terminate TLS at a trusted proxy and set HOMEDEX_SECURE_COOKIES=true.", "address", *listen)
	}
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
		cancelApp()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	case err := <-errCh:
		cancelApp()
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// isLoopbackListen reports whether the listen address is bound to loopback only.
// An empty host (e.g. ":7377") binds every interface, so it is not loopback.
func isLoopbackListen(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
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

func envDays(key string, defaultDays int64) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		value = strconv.FormatInt(defaultDays, 10)
	}
	days, err := strconv.ParseInt(value, 10, 64)
	if err != nil || days <= 0 || days > int64((1<<63-1)/(24*time.Hour)) {
		return 0, fmt.Errorf("%s must be a positive whole number of days", key)
	}
	return time.Duration(days) * 24 * time.Hour, nil
}
