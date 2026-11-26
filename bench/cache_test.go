package bench

import (
	"testing"
	"time"
)

func blocks(keys ...string) []CacheBlock {
	var out []CacheBlock
	for _, k := range keys {
		out = append(out, CacheBlock{Key: k, Tokens: 1000})
	}
	return out
}

func TestCacheSimPrefixAndBreakpoints(t *testing.T) {
	c := NewCacheSim(512)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	b := blocks("sys", "a", "b", "c")
	b[0].Break, b[3].Break = true, true
	r := c.Request(now, b)
	if r.Read != 0 || r.Write != 4000 || r.Uncached != 0 {
		t.Fatalf("first request: %+v", r)
	}
	b2 := blocks("sys", "a", "b", "c", "d", "e")
	b2[0].Break, b2[5].Break = true, true
	r = c.Request(now.Add(time.Minute), b2)
	if r.Read != 4000 || r.Write != 2000 {
		t.Fatalf("lookback should find the previous breakpoint: %+v", r)
	}
	b3 := blocks("sys", "a", "X", "c", "d", "e")
	b3[0].Break, b3[5].Break = true, true
	r = c.Request(now.Add(2*time.Minute), b3)
	if r.Read != 1000 || r.Write != 5000 {
		t.Fatalf("a change in the middle must invalidate everything after it: %+v", r)
	}
	r = c.Request(now.Add(20*time.Minute), b3)
	if r.Read != 0 {
		t.Fatalf("entries must expire after the TTL: %+v", r)
	}
}

func TestCacheSimLookbackLimitAndMinimum(t *testing.T) {
	c := NewCacheSim(1024)
	now := time.Now()
	first := blocks("s", "a")
	first[1].Break = true
	c.Request(now, first)
	var keys []string
	keys = append(keys, "s", "a")
	for i := 0; i < 25; i++ {
		keys = append(keys, string(rune('A'+i)))
	}
	long := blocks(keys...)
	long[len(long)-1].Break = true
	if r := c.Request(now, long); r.Read != 0 {
		t.Fatalf("hit beyond the 20-position lookback: %+v", r)
	}
	small := NewCacheSim(1024)
	tiny := []CacheBlock{{Key: "x", Tokens: 100, Break: true}}
	small.Request(now, tiny)
	if r := small.Request(now, tiny); r.Read != 0 || r.Write != 0 {
		t.Fatalf("prefixes under the minimum must not be cached: %+v", r)
	}
}

func TestGrade(t *testing.T) {
	if !grade("The port is 8443.", []string{"ListenPort = 8443"}) {
		t.Fatal("value with short answer")
	}
	if grade("Port 18443 is used.", []string{"ListenPort = 8443"}) {
		t.Fatal("value must match as a whole token")
	}
	if grade("I do not know", []string{"wal", "crash"}) {
		t.Fatal("false positive")
	}
	if !grade("The test fails with: replay mismatch after crash: expected 54 frames, got 8 (case 7)", []string{"replay mismatch after crash: expected 54 frames, got 8 (case 7)"}) {
		t.Fatal("assertion message")
	}
	if !grade("It reports expected 54 frames but got 8, case 7", []string{"replay mismatch after crash: expected 54 frames, got 8 (case 7)"}) {
		t.Fatal("long key numeric fallback")
	}
	if grade("expected 54 frames, got 9 (case 7)", []string{"replay mismatch after crash: expected 54 frames, got 8 (case 7)"}) {
		t.Fatal("wrong number accepted")
	}
}
