package auth

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"time"
)

// ShareToken is safe to return from list endpoints. The plaintext token is
// only returned by Create and is never persisted.
type ShareToken struct {
	ID        int64      `json:"id"`
	Name      string     `json:"name"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at"`
	Revoked   bool       `json:"revoked"`
	Active    bool       `json:"active"`
}

type CreatedShareToken struct {
	ShareToken
	Token string `json:"token"`
}

type ShareManager struct {
	db  *sql.DB
	now func() time.Time
}

func NewShareManager(db *sql.DB) *ShareManager {
	return &ShareManager{db: db, now: time.Now}
}

func (m *ShareManager) Create(ctx context.Context, name string, expiresAt *time.Time) (CreatedShareToken, error) {
	now := m.now().UTC()
	if expiresAt != nil {
		expiry := expiresAt.UTC()
		if !expiry.After(now) {
			return CreatedShareToken{}, errors.New("share expiry must be in the future")
		}
		expiresAt = &expiry
	}
	token, err := randomToken(16) // 128 bits of entropy.
	if err != nil {
		return CreatedShareToken{}, err
	}
	var expiry any
	if expiresAt != nil {
		expiry = expiresAt.Format(time.RFC3339Nano)
	}
	result, err := m.db.ExecContext(ctx, `INSERT INTO share_tokens(token_hash,name,created_at,expires_at,revoked) VALUES(?,?,?,?,0)`, tokenHash(token), name, now.Format(time.RFC3339Nano), expiry)
	if err != nil {
		return CreatedShareToken{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return CreatedShareToken{}, err
	}
	return CreatedShareToken{ShareToken: ShareToken{ID: id, Name: name, CreatedAt: now, ExpiresAt: expiresAt, Active: true}, Token: token}, nil
}

func (m *ShareManager) List(ctx context.Context) ([]ShareToken, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT id,name,created_at,expires_at,revoked FROM share_tokens ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ShareToken{}
	for rows.Next() {
		item, err := scanShare(rows)
		if err != nil {
			return nil, err
		}
		item.Active = !item.Revoked && (item.ExpiresAt == nil || item.ExpiresAt.After(m.now()))
		items = append(items, item)
	}
	return items, rows.Err()
}

func (m *ShareManager) Validate(ctx context.Context, token string) (ShareToken, error) {
	if token == "" {
		return ShareToken{}, errors.New("missing share token")
	}
	row := m.db.QueryRowContext(ctx, `SELECT id,name,created_at,expires_at,revoked,token_hash FROM share_tokens WHERE token_hash=?`, tokenHash(token))
	var item ShareToken
	var created string
	var expiry sql.NullString
	var storedHash string
	if err := row.Scan(&item.ID, &item.Name, &created, &expiry, &item.Revoked, &storedHash); err != nil {
		return ShareToken{}, err
	}
	// Keep an explicit constant-time comparison at the authentication boundary,
	// even though the indexed lookup already uses the SHA-256 digest.
	want := tokenHash(token)
	if subtle.ConstantTimeCompare([]byte(storedHash), []byte(want)) != 1 {
		return ShareToken{}, errors.New("invalid share token")
	}
	var err error
	item.CreatedAt, err = parseDBTime(created)
	if err != nil {
		return ShareToken{}, err
	}
	if expiry.Valid {
		t, err := parseDBTime(expiry.String)
		if err != nil {
			return ShareToken{}, err
		}
		item.ExpiresAt = &t
	}
	if item.Revoked {
		return ShareToken{}, errors.New("share token revoked")
	}
	if item.ExpiresAt != nil && !item.ExpiresAt.After(m.now()) {
		return ShareToken{}, errors.New("share token expired")
	}
	item.Active = true
	return item, nil
}

func (m *ShareManager) Revoke(ctx context.Context, id int64) error {
	result, err := m.db.ExecContext(ctx, `UPDATE share_tokens SET revoked=1 WHERE id=?`, id)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

type rowScanner interface{ Scan(...any) error }

func scanShare(row rowScanner) (ShareToken, error) {
	var item ShareToken
	var created string
	var expiry sql.NullString
	if err := row.Scan(&item.ID, &item.Name, &created, &expiry, &item.Revoked); err != nil {
		return item, err
	}
	var err error
	item.CreatedAt, err = parseDBTime(created)
	if err != nil {
		return item, err
	}
	if expiry.Valid {
		t, err := parseDBTime(expiry.String)
		if err != nil {
			return item, err
		}
		item.ExpiresAt = &t
	}
	return item, nil
}

func parseDBTime(value string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02 15:04:05", value)
}
