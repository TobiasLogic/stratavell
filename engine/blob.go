package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/TobiasLogic/palimpsest/internal/store"
)

func (e *Engine) putBlob(ctx context.Context, tx *sql.Tx, hash string, data []byte) error {
	if e.artifacts == nil {
		got, _, err := store.PutBlob(ctx, tx, data, e.blobPol)
		if err != nil {
			return err
		}
		if got != hash {
			return fmt.Errorf("engine: blob hash mismatch")
		}
		return nil
	}
	var exists int
	err := tx.QueryRowContext(ctx, "SELECT 1 FROM blobs WHERE hash=?", hash).Scan(&exists)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err := e.artifacts.Put(ctx, hash, data); err != nil {
		return fmt.Errorf("engine: artifact store: %w", err)
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO blobs(hash,size,codec,stored_size,data,created_at) VALUES(?,?,?,?,?,?)", hash, len(data), "external", 0, []byte{}, time.Now().UnixNano())
	return err
}

func (e *Engine) getBlob(ctx context.Context, q queryer, hash string) ([]byte, error) {
	var codec string
	err := q.QueryRowContext(ctx, "SELECT codec FROM blobs WHERE hash=?", hash).Scan(&codec)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s", store.ErrBlobNotFound, hash)
	}
	if err != nil {
		return nil, err
	}
	if codec != "external" {
		return store.GetBlob(ctx, q, hash)
	}
	if e.artifacts == nil {
		return nil, fmt.Errorf("engine: blob %s lives in an external artifact store that is not configured", short(hash))
	}
	data, err := e.artifacts.Get(ctx, hash)
	if err != nil {
		return nil, err
	}
	if store.HashBytes(data) != hash {
		return nil, fmt.Errorf("%w: %s", store.ErrBlobCorrupt, hash)
	}
	return data, nil
}
