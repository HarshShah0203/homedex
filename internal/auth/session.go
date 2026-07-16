package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"
)

type Session struct {
	UserRef, Token, CSRF string
	ExpiresAt            time.Time
}
type SessionManager struct {
	db  *sql.DB
	ttl time.Duration
	now func() time.Time
}

func NewSessionManager(db *sql.DB, ttl time.Duration) *SessionManager {
	return &SessionManager{db: db, ttl: ttl, now: time.Now}
}

func (m *SessionManager) Create(ctx context.Context, user string) (Session, error) {
	token, err := randomToken(32)
	if err != nil {
		return Session{}, err
	}
	csrf, err := randomToken(32)
	if err != nil {
		return Session{}, err
	}
	now := m.now().UTC()
	expiry := now.Add(m.ttl)
	_, err = m.db.ExecContext(ctx, `INSERT INTO sessions(user_ref,token_hash,csrf_hash,created_at,expires_at) VALUES(?,?,?,?,?)`, user, tokenHash(token), tokenHash(csrf), now.Format(time.RFC3339Nano), expiry.Format(time.RFC3339Nano))
	if err != nil {
		return Session{}, err
	}
	return Session{UserRef: user, Token: token, CSRF: csrf, ExpiresAt: expiry}, nil
}

func (m *SessionManager) Validate(ctx context.Context, token string) (Session, error) {
	if token == "" {
		return Session{}, errors.New("missing session token")
	}
	var s Session
	var expiry string
	err := m.db.QueryRowContext(ctx, `SELECT user_ref,expires_at FROM sessions WHERE token_hash=?`, tokenHash(token)).Scan(&s.UserRef, &expiry)
	if err != nil {
		return Session{}, err
	}
	s.ExpiresAt, err = time.Parse(time.RFC3339Nano, expiry)
	if err != nil {
		return Session{}, err
	}
	if !s.ExpiresAt.After(m.now()) {
		return Session{}, errors.New("session expired")
	}
	s.Token = token
	return s, nil
}

func (m *SessionManager) ValidateCSRF(ctx context.Context, token, csrf string) bool {
	var count int
	err := m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE token_hash=? AND csrf_hash=? AND expires_at>?`, tokenHash(token), tokenHash(csrf), m.now().UTC().Format(time.RFC3339Nano)).Scan(&count)
	return err == nil && count == 1
}
func (m *SessionManager) Delete(ctx context.Context, token string) error {
	_, err := m.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash=?`, tokenHash(token))
	return err
}
func tokenHash(v string) string { sum := sha256.Sum256([]byte(v)); return hex.EncodeToString(sum[:]) }
func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
