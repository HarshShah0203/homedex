package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/HarshShah0203/homedex/internal/auth"
)

// ConnectorConfigs is the only persistence boundary for connector config.
// Plaintext JSON is authenticated-encrypted before it reaches SQLite.
type ConnectorConfigs struct {
	store *Store
	box   *auth.SecretBox
}

type ConnectorRecord struct {
	ID              int64  `json:"id"`
	Kind            string `json:"kind"`
	Name            string `json:"name"`
	Enabled         bool   `json:"enabled"`
	ScheduleMinutes int    `json:"schedule_minutes"`
	LastStatus      string `json:"last_status"`
	LastError       string `json:"last_error"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

func NewConnectorConfigs(s *Store, box *auth.SecretBox) *ConnectorConfigs {
	return &ConnectorConfigs{store: s, box: box}
}

func (c *ConnectorConfigs) Create(ctx context.Context, kind, name string, config any) (int64, error) {
	plain, err := json.Marshal(config)
	if err != nil {
		return 0, fmt.Errorf("encode connector config: %w", err)
	}
	encrypted, err := c.box.Seal(plain)
	if err != nil {
		return 0, fmt.Errorf("encrypt connector config: %w", err)
	}
	return c.store.CreateConnector(ctx, kind, name, encrypted)
}

func (c *ConnectorConfigs) Load(ctx context.Context, id int64, target any) error {
	var encrypted []byte
	if err := c.store.DB().QueryRowContext(ctx, `SELECT config_encrypted FROM connectors WHERE id=?`, id).Scan(&encrypted); err != nil {
		return err
	}
	plain, err := c.box.Open(encrypted)
	if err != nil {
		return fmt.Errorf("decrypt connector config: %w", err)
	}
	if err := json.Unmarshal(plain, target); err != nil {
		return fmt.Errorf("decode connector config: %w", err)
	}
	return nil
}

func (c *ConnectorConfigs) Update(ctx context.Context, id int64, name string, config any, enabled bool, schedule int) error {
	if schedule <= 0 {
		return fmt.Errorf("schedule_minutes must be positive")
	}
	plain, err := json.Marshal(config)
	if err != nil {
		return err
	}
	encrypted, err := c.box.Seal(plain)
	if err != nil {
		return err
	}
	r, err := c.store.DB().ExecContext(ctx, `UPDATE connectors SET name=?,config_encrypted=?,enabled=?,schedule_minutes=?,updated_at=CURRENT_TIMESTAMP WHERE id=?`, name, encrypted, enabled, schedule, id)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
func (c *ConnectorConfigs) Delete(ctx context.Context, id int64) error {
	r, e := c.store.DB().ExecContext(ctx, `DELETE FROM connectors WHERE id=?`, id)
	if e != nil {
		return e
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
func (c *ConnectorConfigs) Record(ctx context.Context, id int64) (ConnectorRecord, error) {
	var x ConnectorRecord
	e := c.store.DB().QueryRowContext(ctx, `SELECT id,kind,name,enabled,schedule_minutes,last_status,last_error,created_at,updated_at FROM connectors WHERE id=?`, id).Scan(&x.ID, &x.Kind, &x.Name, &x.Enabled, &x.ScheduleMinutes, &x.LastStatus, &x.LastError, &x.CreatedAt, &x.UpdatedAt)
	return x, e
}
func (c *ConnectorConfigs) List(ctx context.Context) ([]ConnectorRecord, error) {
	rows, e := c.store.DB().QueryContext(ctx, `SELECT id,kind,name,enabled,schedule_minutes,last_status,last_error,created_at,updated_at FROM connectors ORDER BY id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []ConnectorRecord{}
	for rows.Next() {
		var x ConnectorRecord
		if e = rows.Scan(&x.ID, &x.Kind, &x.Name, &x.Enabled, &x.ScheduleMinutes, &x.LastStatus, &x.LastError, &x.CreatedAt, &x.UpdatedAt); e != nil {
			return nil, e
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
