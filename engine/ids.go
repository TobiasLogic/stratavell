package engine

import (
	"crypto/rand"
	"encoding/binary"
	mrand "math/rand/v2"
	"sync"
	"time"
)

type Clock interface {
	Now() time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

type ManualClock struct {
	mu sync.Mutex
	t  time.Time
}

func NewManualClock(t time.Time) *ManualClock { return &ManualClock{t: t} }

func (c *ManualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *ManualClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type IDSource interface {
	New(prefix string) string
}

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

type ulidSource struct {
	mu    sync.Mutex
	clock Clock
	last  uint64
	seq   uint64
	rnd   func([]byte)
}

func newULIDSource(clock Clock) *ulidSource {
	return &ulidSource{clock: clock, rnd: func(b []byte) { _, _ = rand.Read(b) }}
}

func NewSeededIDs(clock Clock, seed uint64) IDSource {
	r := mrand.New(mrand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	var mu sync.Mutex
	return &ulidSource{clock: clock, rnd: func(b []byte) {
		mu.Lock()
		defer mu.Unlock()
		for i := range b {
			b[i] = byte(r.UintN(256))
		}
	}}
}

func (s *ulidSource) New(prefix string) string {
	s.mu.Lock()
	ms := uint64(s.clock.Now().UnixMilli())
	if ms <= s.last {
		ms = s.last
		s.seq++
	} else {
		s.last = ms
		s.seq = 0
	}
	seq := s.seq
	s.mu.Unlock()
	var r [8]byte
	s.rnd(r[:])
	lo := binary.BigEndian.Uint64(r[:]) & (uint64(1)<<40 - 1)
	lo |= (seq & 0xFFFFFF) << 40
	var out [26]byte
	v := ms
	for i := 9; i >= 0; i-- {
		out[i] = crockford[v&31]
		v >>= 5
	}
	x := lo
	for i := 25; i >= 10; i-- {
		out[i] = crockford[x&31]
		x >>= 5
	}
	return prefix + string(out[:])
}
