package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type Durability string

const (
	DurabilityFull   Durability = "full"
	DurabilityNormal Durability = "normal"
)

type Options struct {
	Durability   Durability
	ReadConns    int
	SecureDelete bool
	BusyTimeout  time.Duration
}

type DB struct {
	W    *sql.DB
	R    *sql.DB
	Path string
	wmu  sync.Mutex

	statsMu sync.Mutex
	txCount int64
	txTime  time.Duration
}

func dsn(path string, readOnly bool, o Options) string {
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "foreign_keys(ON)")
	q.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", o.BusyTimeout.Milliseconds()))
	if o.Durability == DurabilityNormal {
		q.Add("_pragma", "synchronous(NORMAL)")
	} else {
		q.Add("_pragma", "synchronous(FULL)")
	}
	if o.SecureDelete {
		q.Add("_pragma", "secure_delete(ON)")
	}
	if readOnly {
		q.Add("_pragma", "query_only(1)")
	} else {
		q.Add("_txlock", "immediate")
	}
	return "file:" + path + "?" + q.Encode()
}

func Open(ctx context.Context, path string, o Options) (*DB, error) {
	if o.ReadConns <= 0 {
		o.ReadConns = 4
	}
	if o.BusyTimeout <= 0 {
		o.BusyTimeout = 10 * time.Second
	}
	if o.Durability == "" {
		o.Durability = DurabilityFull
	}
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, err
		}
	}
	w, err := sql.Open("sqlite", dsn(path, false, o))
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	w.SetMaxIdleConns(1)
	w.SetConnMaxLifetime(0)
	db := &DB{W: w, Path: path}
	if err := db.migrate(ctx); err != nil {
		w.Close()
		return nil, err
	}
	r, err := sql.Open("sqlite", dsn(path, true, o))
	if err != nil {
		w.Close()
		return nil, err
	}
	r.SetMaxOpenConns(o.ReadConns)
	r.SetMaxIdleConns(o.ReadConns)
	db.R = r
	if err := r.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func (db *DB) Close() error {
	var errs []error
	if db.R != nil {
		errs = append(errs, db.R.Close())
	}
	if db.W != nil {
		_, _ = db.W.Exec("PRAGMA wal_checkpoint(PASSIVE)")
		errs = append(errs, db.W.Close())
	}
	return errors.Join(errs...)
}

func (db *DB) SchemaVersion(ctx context.Context) (int, error) {
	var v int
	err := db.W.QueryRowContext(ctx, "SELECT COALESCE(MAX(version),0) FROM schema_migrations").Scan(&v)
	return v, err
}

func (db *DB) migrate(ctx context.Context) error {
	if _, err := db.W.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations(version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at INTEGER NOT NULL)"); err != nil {
		return err
	}
	cur, err := db.SchemaVersion(ctx)
	if err != nil {
		return err
	}
	if cur > LatestVersion() {
		return fmt.Errorf("store: database schema %d is newer than this build (%d)", cur, LatestVersion())
	}
	for _, m := range migrations {
		if m.version <= cur {
			continue
		}
		err := db.Tx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, m.sql); err != nil {
				return fmt.Errorf("migration %d (%s): %w", m.version, m.name, err)
			}
			_, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version,name,applied_at) VALUES(?,?,?)", m.version, m.name, time.Now().UnixNano())
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func LatestVersion() int { return migrations[len(migrations)-1].version }

var ErrRetry = errors.New("store: transient error")

func isBusy(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "SQLITE_BUSY") || strings.Contains(s, "database is locked")
}

func (db *DB) Tx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	db.wmu.Lock()
	defer db.wmu.Unlock()
	start := time.Now()
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		err = db.tx(ctx, fn)
		if !isBusy(err) {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt+1) * 20 * time.Millisecond):
		}
	}
	db.statsMu.Lock()
	db.txCount++
	db.txTime += time.Since(start)
	db.statsMu.Unlock()
	return err
}

func (db *DB) tx(ctx context.Context, fn func(tx *sql.Tx) error) (err error) {
	tx, err := db.W.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (db *DB) TxStats() (int64, time.Duration) {
	db.statsMu.Lock()
	defer db.statsMu.Unlock()
	return db.txCount, db.txTime
}

type SizeInfo struct {
	PageCount int64
	PageSize  int64
	Freelist  int64
	WALBytes  int64
	FileBytes int64
}

func (db *DB) Size(ctx context.Context) (SizeInfo, error) {
	var s SizeInfo
	if err := db.R.QueryRowContext(ctx, "PRAGMA page_count").Scan(&s.PageCount); err != nil {
		return s, err
	}
	if err := db.R.QueryRowContext(ctx, "PRAGMA page_size").Scan(&s.PageSize); err != nil {
		return s, err
	}
	if err := db.R.QueryRowContext(ctx, "PRAGMA freelist_count").Scan(&s.Freelist); err != nil {
		return s, err
	}
	if fi, err := os.Stat(db.Path); err == nil {
		s.FileBytes = fi.Size()
	}
	if fi, err := os.Stat(db.Path + "-wal"); err == nil {
		s.WALBytes = fi.Size()
	}
	return s, nil
}

func (db *DB) Checkpoint(ctx context.Context) error {
	db.wmu.Lock()
	defer db.wmu.Unlock()
	_, err := db.W.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
	return err
}

func (db *DB) Vacuum(ctx context.Context) error {
	db.wmu.Lock()
	defer db.wmu.Unlock()
	_, err := db.W.ExecContext(ctx, "VACUUM")
	return err
}

func (db *DB) IntegrityCheck(ctx context.Context) (string, error) {
	c, err := sql.Open("sqlite", dsn(db.Path, true, Options{BusyTimeout: 10 * time.Second}))
	if err != nil {
		return "", err
	}
	defer c.Close()
	var s string
	if err := c.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&s); err != nil {
		return "", err
	}
	if s != "ok" {
		return s, nil
	}
	db.wmu.Lock()
	defer db.wmu.Unlock()
	if _, err := db.W.ExecContext(ctx, "INSERT INTO docs_fts(docs_fts, rank) VALUES('integrity-check', 1)"); err != nil {
		return "fts5: " + err.Error(), nil
	}
	return "ok", nil
}
