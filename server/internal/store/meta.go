package store

import (
	"context"
	"database/sql"
	"errors"
)

// ErrNoMeta is returned by GetMeta for a key that is not set.
var ErrNoMeta = errors.New("store: no such meta key")

// GetMeta reads a value from the meta table.
func (db *DB) GetMeta(ctx context.Context, key string) (any, error) {
	return getMeta(ctx, db.r.QueryRowContext, key)
}

// SetMeta writes a value to the meta table (text, integer, real or blob).
func (db *DB) SetMeta(ctx context.Context, key string, value any) error {
	_, err := db.Exec(ctx, "INSERT INTO meta (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", key, value)
	return err
}

type queryRower func(ctx context.Context, query string, args ...any) *sql.Row

func getMeta(ctx context.Context, qr queryRower, key string) (any, error) {
	var v any
	err := qr(ctx, "SELECT value FROM meta WHERE key = ?", key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoMeta
	}
	return v, err
}

// metaBlob reads a blob value; a missing key is ErrNoMeta, a non-blob value an error.
func metaBlob(ctx context.Context, qr queryRower, key string) ([]byte, error) {
	v, err := getMeta(ctx, qr, key)
	if err != nil {
		return nil, err
	}
	b, ok := v.([]byte)
	if !ok {
		return nil, errors.New("store: meta " + key + " is not a blob")
	}
	return b, nil
}
