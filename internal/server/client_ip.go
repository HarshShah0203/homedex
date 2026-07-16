package server

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// TrustedProxySet is an explicit allowlist of reverse-proxy socket peers.
// Forwarding headers are ignored unless the immediate peer is in this set.
type TrustedProxySet struct {
	prefixes []netip.Prefix
}

// ParseTrustedProxies parses a comma-separated list of IP addresses or CIDRs.
// A bare address is treated as a single-host /32 or /128 prefix.
func ParseTrustedProxies(value string) (TrustedProxySet, error) {
	var set TrustedProxySet
	for _, raw := range strings.Split(value, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(raw)
		if err != nil {
			addr, addrErr := netip.ParseAddr(raw)
			if addrErr != nil {
				return TrustedProxySet{}, fmt.Errorf("invalid trusted proxy %q", raw)
			}
			addr = addr.Unmap()
			prefix = netip.PrefixFrom(addr, addr.BitLen())
		}
		set.prefixes = append(set.prefixes, prefix.Masked())
	}
	return set, nil
}

func (s TrustedProxySet) contains(addr netip.Addr) bool {
	addr = addr.Unmap()
	for _, prefix := range s.prefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func (s TrustedProxySet) clientIP(r *http.Request) string {
	peer, ok := parsePeerAddr(r.RemoteAddr)
	if !ok {
		return "unknown"
	}
	if !s.contains(peer) {
		return peer.String()
	}
	if forwarded := r.Header.Values("X-Forwarded-For"); len(forwarded) > 0 {
		chain, valid := parseForwardedChain(forwarded)
		if !valid {
			return peer.String()
		}
		client := peer
		for i := len(chain) - 1; i >= 0 && s.contains(client); i-- {
			client = chain[i]
		}
		return client.String()
	}
	if raw := strings.TrimSpace(r.Header.Get("X-Real-IP")); raw != "" {
		if addr, err := netip.ParseAddr(raw); err == nil {
			return addr.Unmap().String()
		}
	}
	return peer.String()
}

func parsePeerAddr(remote string) (netip.Addr, bool) {
	if addrPort, err := netip.ParseAddrPort(remote); err == nil {
		return addrPort.Addr().Unmap(), true
	}
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = strings.Trim(remote, "[]")
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}

func parseForwardedChain(values []string) ([]netip.Addr, bool) {
	var chain []netip.Addr
	for _, value := range values {
		for _, raw := range strings.Split(value, ",") {
			addr, err := netip.ParseAddr(strings.TrimSpace(raw))
			if err != nil {
				return nil, false
			}
			chain = append(chain, addr.Unmap())
		}
	}
	return chain, len(chain) > 0
}
