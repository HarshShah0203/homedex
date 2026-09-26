package server

import (
	"context"
	"fmt"

	"github.com/HarshShah0203/homedex/internal/auth"
	"github.com/HarshShah0203/homedex/internal/store"
)

// AdminBootstrap reports what BootstrapAdmin did with a configured password.
type AdminBootstrap int

const (
	// AdminCreated means no admin existed and the password is now the admin
	// password, exactly as if it had been entered in the setup wizard.
	AdminCreated AdminBootstrap = iota + 1
	// AdminAlreadyExists means an admin password was already stored; the
	// configured password was ignored and nothing was changed.
	AdminAlreadyExists
)

// BootstrapAdmin stores the first admin password when no admin exists yet, so
// an instance published on a LAN (for example by a homelab app store) is never
// waiting for whoever reaches the setup page first. The password is validated
// and hashed exactly as POST /api/setup does. An existing admin is never
// replaced. Errors never contain the password.
func BootstrapAdmin(ctx context.Context, st *store.Store, password string) (AdminBootstrap, error) {
	var count int
	if err := st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM settings WHERE key='admin_password_hash'`).Scan(&count); err != nil {
		return 0, fmt.Errorf("check for an existing admin: %w", err)
	}
	if count > 0 {
		return AdminAlreadyExists, nil
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return 0, err
	}
	// DO NOTHING keeps an admin that appeared since the check above, for
	// example one created by another process sharing the database.
	result, err := st.DB().ExecContext(ctx, `INSERT INTO settings(key,value) VALUES('admin_password_hash',?) ON CONFLICT(key) DO NOTHING`, hash)
	if err != nil {
		return 0, fmt.Errorf("store the admin password hash: %w", err)
	}
	if inserted, err := result.RowsAffected(); err == nil && inserted == 0 {
		return AdminAlreadyExists, nil
	}
	return AdminCreated, nil
}
