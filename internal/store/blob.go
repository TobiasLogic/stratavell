package store

import (
	"bytes"
	"compress/flate"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"
)

const (
	CodecRaw     = "raw"
	CodecDeflate = "deflate"
)

type BlobPolicy struct {
	CompressMin   int
	CompressLevel int
	MaxRatio      float64
}

func DefaultBlobPolicy() BlobPolicy {
	return BlobPolicy{CompressMin: 4096, CompressLevel: flate.BestSpeed, MaxRatio: 0.85}
}

var (
	ErrBlobNotFound = errors.New("store: blob not found")
	ErrBlobCorrupt  = errors.New("store: blob content does not match its hash")
)

func HashBytes(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func encode(data []byte, p BlobPolicy) (string, []byte, error) {
	if p.CompressMin <= 0 || len(data) < p.CompressMin {
		return CodecRaw, data, nil
	}
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, p.CompressLevel)
	if err != nil {
		return "", nil, err
	}
	if _, err := w.Write(data); err != nil {
		return "", nil, err
	}
	if err := w.Close(); err != nil {
		return "", nil, err
	}
	if float64(buf.Len()) > p.MaxRatio*float64(len(data)) {
		return CodecRaw, data, nil
	}
	return CodecDeflate, buf.Bytes(), nil
}

func decode(codec string, data []byte, size int64) ([]byte, error) {
	switch codec {
	case CodecRaw:
		return data, nil
	case CodecDeflate:
		r := flate.NewReader(bytes.NewReader(data))
		defer r.Close()
		out := make([]byte, 0, size)
		buf := bytes.NewBuffer(out)
		if _, err := io.Copy(buf, io.LimitReader(r, size+1)); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrBlobCorrupt, err)
		}
		return buf.Bytes(), nil
	default:
		return nil, fmt.Errorf("store: unknown codec %q", codec)
	}
}

func PutBlob(ctx context.Context, tx *sql.Tx, data []byte, p BlobPolicy) (string, bool, error) {
	h := HashBytes(data)
	var exists int
	err := tx.QueryRowContext(ctx, "SELECT 1 FROM blobs WHERE hash=?", h).Scan(&exists)
	if err == nil {
		return h, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", false, err
	}
	codec, enc, err := encode(data, p)
	if err != nil {
		return "", false, err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO blobs(hash,size,codec,stored_size,data,created_at) VALUES(?,?,?,?,?,?)",
		h, len(data), codec, len(enc), enc, time.Now().UnixNano())
	if err != nil {
		return "", false, err
	}
	return h, true, nil
}

type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func GetBlob(ctx context.Context, q querier, hash string) ([]byte, error) {
	var codec string
	var size int64
	var data []byte
	err := q.QueryRowContext(ctx, "SELECT codec,size,data FROM blobs WHERE hash=?", hash).Scan(&codec, &size, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s", ErrBlobNotFound, hash)
	}
	if err != nil {
		return nil, err
	}
	out, err := decode(codec, data, size)
	if err != nil {
		return nil, err
	}
	if int64(len(out)) != size || HashBytes(out) != hash {
		return nil, fmt.Errorf("%w: %s", ErrBlobCorrupt, hash)
	}
	return out, nil
}

func GCBlobs(ctx context.Context, tx *sql.Tx) (int64, error) {
	res, err := tx.ExecContext(ctx, "DELETE FROM blobs WHERE NOT EXISTS (SELECT 1 FROM blob_refs r WHERE r.blob_hash = blobs.hash)")
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
