package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// runHealthcheck implements `homedex healthcheck`: it asks the running server
// on this machine for /api/health and exits 0 only when it answers ok. The
// distroless image has no shell, curl or wget, so container runtimes and app
// store health checks call the binary itself.
func runHealthcheck(args []string, stderr io.Writer) int {
	flags := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
	flags.SetOutput(stderr)
	listen := flags.String("listen", envString("HOMEDEX_LISTEN", ":7377"), "listen address of the Homedex server to probe")
	timeout := flags.Duration("timeout", 3*time.Second, "how long to wait for an answer")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	target, err := healthURL(*listen)
	if err != nil {
		fmt.Fprintln(stderr, "healthcheck:", err)
		return 1
	}
	client := &http.Client{
		Timeout:   *timeout,
		Transport: &http.Transport{Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Get(target)
	if err != nil {
		fmt.Fprintln(stderr, "healthcheck:", err)
		return 1
	}
	defer resp.Body.Close()
	var body struct {
		Status string `json:"status"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&body) != nil || body.Status != "ok" {
		fmt.Fprintf(stderr, "healthcheck: %s answered %d\n", target, resp.StatusCode)
		return 1
	}
	return 0
}

// healthURL turns a listen address into the URL to probe on this machine. A
// wildcard or empty host is probed on loopback.
func healthURL(listen string) (string, error) {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "", fmt.Errorf("parse listen address %q: %w", listen, err)
	}
	if port == "" {
		return "", fmt.Errorf("listen address %q has no port", listen)
	}
	switch host {
	case "", "0.0.0.0", "::":
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/api/health", nil
}
