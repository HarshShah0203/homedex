package tailscale

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// cachedToken is an OAuth access token held in memory only; it is never
// persisted or logged.
type cachedToken struct {
	value   string
	expires time.Time
}

// bearer returns the credential for the device listing: the API access token
// as is, or an OAuth access token from the cache or a fresh exchange.
func (c *Connector) bearer(ctx context.Context, x config, fresh bool) (string, error) {
	if !x.oauth() {
		return x.APIKey, nil
	}
	key := tokenKey(x)
	if !fresh {
		c.mu.Lock()
		tok, ok := c.tokens[key]
		c.mu.Unlock()
		if ok && c.clock().Before(tok.expires) {
			return tok.value, nil
		}
	}
	tok, err := c.exchange(ctx, x)
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	if c.tokens == nil {
		c.tokens = map[string]cachedToken{}
	}
	c.tokens[key] = tok
	c.mu.Unlock()
	return tok.value, nil
}

// tokenKey fingerprints the secret so an edited secret misses the cache
// without the map holding the secret itself.
func tokenKey(x config) string {
	sum := sha256.Sum256([]byte(x.OAuthClientSecret))
	return x.BaseURL + "|" + x.OAuthClientID + "|" + hex.EncodeToString(sum[:])[:16]
}

func (c *Connector) forget(x config) {
	c.mu.Lock()
	delete(c.tokens, tokenKey(x))
	c.mu.Unlock()
}

// exchange runs the OAuth client-credentials grant. It always asks for the
// read scope alone, so a client granted more still yields a read-only token.
func (c *Connector) exchange(ctx context.Context, x config) (cachedToken, error) {
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {x.OAuthClientID},
		"client_secret": {x.OAuthClientSecret},
		"scope":         {readScope},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, x.BaseURL+tokenPath, strings.NewReader(form.Encode()))
	if err != nil {
		return cachedToken{}, fmt.Errorf("Tailscale OAuth token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	res, err := c.client().Do(req)
	if err != nil {
		return cachedToken{}, fmt.Errorf("Tailscale OAuth token request: %w", err)
	}
	defer res.Body.Close()
	// The body is never read on failure: an upstream may echo the form back.
	switch {
	case res.StatusCode/100 == 3:
		return cachedToken{}, fmt.Errorf("Tailscale OAuth token endpoint redirected (%s); Homedex does not follow redirects with credentials, check base_url", statusText(res.StatusCode))
	case res.StatusCode/100 != 2:
		return cachedToken{}, fmt.Errorf("Tailscale OAuth token request returned %s: check the client ID and secret, and that the OAuth client grants %s", statusText(res.StatusCode), readScope)
	}
	var body struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err = json.NewDecoder(io.LimitReader(res.Body, maxTokenBytes)).Decode(&body); err != nil {
		return cachedToken{}, fmt.Errorf("decode Tailscale OAuth token response: %w", err)
	}
	switch {
	case body.AccessToken == "":
		return cachedToken{}, errors.New("Tailscale OAuth token response has no access_token")
	case hasSpaceOrControl(body.AccessToken):
		return cachedToken{}, errors.New("Tailscale OAuth token response has a malformed access_token")
	case body.TokenType != "" && !strings.EqualFold(body.TokenType, "bearer"):
		return cachedToken{}, errors.New("Tailscale OAuth token response is not a Bearer token")
	}
	ttl := time.Duration(body.ExpiresIn) * time.Second
	if body.ExpiresIn <= 0 || body.ExpiresIn > 24*60*60 {
		ttl = time.Hour
	}
	// A minute of slack keeps a token from expiring between check and use.
	return cachedToken{value: body.AccessToken, expires: c.clock().Add(ttl - time.Minute)}, nil
}
