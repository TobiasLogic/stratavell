package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/TobiasLogic/palimpsest/internal/store"
	"github.com/TobiasLogic/palimpsest/internal/tokens"
)

type Tokenizer interface {
	Count(text string) int
	Name() string
}

type estimator struct{}

func (estimator) Count(s string) int { return tokens.Estimate(s) }
func (estimator) Name() string       { return "palimpsest-estimate-v1" }

func DefaultTokenizer() Tokenizer { return estimator{} }

type ArtifactStore interface {
	Put(ctx context.Context, hash string, data []byte) error
	Get(ctx context.Context, hash string) ([]byte, error)
	Delete(ctx context.Context, hash string) error
}

type FileArtifactStore struct {
	Dir string
}

func NewFileArtifactStore(dir string) (*FileArtifactStore, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &FileArtifactStore{Dir: dir}, nil
}

func (f *FileArtifactStore) path(hash string) (string, error) {
	if len(hash) != 64 || strings.Trim(hash, "0123456789abcdef") != "" {
		return "", fmt.Errorf("engine: invalid artifact hash")
	}
	return filepath.Join(f.Dir, hash[:2], hash), nil
}

func (f *FileArtifactStore) Put(ctx context.Context, hash string, data []byte) error {
	p, err := f.path(hash)
	if err != nil {
		return err
	}
	if _, err := os.Stat(p); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), p)
}

func (f *FileArtifactStore) Get(ctx context.Context, hash string) ([]byte, error) {
	p, err := f.path(hash)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", store.ErrBlobNotFound, hash)
	}
	return b, err
}

func (f *FileArtifactStore) Delete(ctx context.Context, hash string) error {
	p, err := f.path(hash)
	if err != nil {
		return err
	}
	err = os.Remove(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

type Candidate struct {
	List        string
	Rank        int
	RefKind     string
	RefID       string
	Agent       string
	Seq         int64
	TS          int64
	Tool        string
	Reliability int
	Stale       bool
	OwnAgent    bool
	HeadSeq     int64
}

type Ranker interface {
	Rank(query string, lists map[string][]Candidate) map[string]float64
}

type RRFRanker struct {
	K       float64
	Weights map[string]float64
}

func DefaultRanker() Ranker {
	return RRFRanker{K: 60, Weights: map[string]float64{"fts": 1.0, "symbol": 0.8, "path": 0.8, "id": 2.0, "vector": 0.7}}
}

func (r RRFRanker) Rank(query string, lists map[string][]Candidate) map[string]float64 {
	out := map[string]float64{}
	meta := map[string]Candidate{}
	for name, l := range lists {
		w, ok := r.Weights[name]
		if !ok {
			w = 0.5
		}
		for _, c := range l {
			out[c.RefID] += w / (r.K + float64(c.Rank))
			if _, ok := meta[c.RefID]; !ok {
				meta[c.RefID] = c
			}
		}
	}
	for id, s := range out {
		c := meta[id]
		switch c.Reliability {
		case 3:
		case 2:
			s *= 0.85
		default:
			s *= 0.75
		}
		if c.Stale {
			s *= 0.6
		}
		if c.OwnAgent && c.HeadSeq > 0 {
			s *= 1 + 0.1*float64(c.Seq)/float64(c.HeadSeq)
		}
		out[id] = s
	}
	return out
}

type Pressure struct {
	Tokens        int
	Capacity      int
	Usage         float64
	GrowthPerTurn float64
	TurnsNeeded   float64
	Samples       int
	Inflight      int
	Thresholds    Thresholds
}

type Trigger interface {
	ShouldCompact(p Pressure) bool
}

type ThresholdTrigger struct{}

func (ThresholdTrigger) ShouldCompact(p Pressure) bool {
	th := p.Thresholds
	c := float64(p.Capacity)
	if p.Usage >= th.Soft {
		return true
	}
	if p.GrowthPerTurn <= 0 || p.Samples < 3 {
		return false
	}
	left := (th.Emergency*c - float64(p.Tokens)) / p.GrowthPerTurn
	return left <= p.TurnsNeeded && p.Usage >= th.Target
}
