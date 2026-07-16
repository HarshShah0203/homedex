package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Store struct{ db *sql.DB }

func Open(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("database path is required")
	}
	dsn := path
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	dsn += sep + "_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(8)
	s := &Store{db: db}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) DB() *sql.DB  { return s.db }
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		return fmt.Errorf("disable foreign keys for migrations: %w", err)
	}
	defer func() {
		restoreCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = conn.ExecContext(restoreCtx, `PRAGMA foreign_keys=ON`)
	}()
	if _, err = conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		return err
	}
	entries, err := fs.ReadDir(migrations, "migrations")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		prefix := strings.SplitN(entry.Name(), "_", 2)[0]
		version, err := strconv.Atoi(prefix)
		if err != nil {
			return fmt.Errorf("invalid migration %q", entry.Name())
		}
		var exists int
		if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version=?`, version).Scan(&exists); err != nil {
			return err
		}
		if exists != 0 {
			continue
		}
		body, err := migrations.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return err
		}
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, string(body)); err == nil {
			_, err = tx.ExecContext(ctx, `INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)`, version, time.Now().UTC().Format(time.RFC3339Nano))
		}
		if err == nil {
			err = checkForeignKeys(ctx, tx)
		}
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", entry.Name(), err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	if _, err = conn.ExecContext(ctx, `PRAGMA foreign_keys=ON`); err != nil {
		return fmt.Errorf("restore foreign keys after migrations: %w", err)
	}
	var enabled int
	if err = conn.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&enabled); err != nil {
		return err
	}
	if enabled != 1 {
		return errors.New("foreign keys remained disabled after migrations")
	}
	return nil
}

func checkForeignKeys(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		var table, parent string
		var rowID any
		var foreignKey int
		if err = rows.Scan(&table, &rowID, &parent, &foreignKey); err != nil {
			return err
		}
		return fmt.Errorf("foreign key check failed for %s row %v referencing %s", table, rowID, parent)
	}
	return rows.Err()
}

func (s *Store) CreateConnector(ctx context.Context, kind, name string, encrypted []byte) (int64, error) {
	if encrypted == nil {
		encrypted = []byte{}
	}
	r, err := s.db.ExecContext(ctx, `INSERT INTO connectors(kind,name,config_encrypted) VALUES(?,?,?)`, kind, name, encrypted)
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}
