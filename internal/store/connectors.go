package store

import (
	"context"
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
