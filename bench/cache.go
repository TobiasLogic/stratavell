package bench

import (
	"hash/fnv"
	"time"
)

type CacheBlock struct {
	Key    string
	Tokens int
	Break  bool
	Group  string
}

type CacheResult struct {
	Read     int
	Write    int
	Uncached int
}

type CacheSim struct {
	TTL       time.Duration
	Lookback  int
	MinTokens int
	entries   map[uint64]time.Time
}

func NewCacheSim(minTokens int) *CacheSim {
	return &CacheSim{TTL: 5 * time.Minute, Lookback: 20, MinTokens: minTokens, entries: map[uint64]time.Time{}}
}

func (c *CacheSim) Request(now time.Time, blocks []CacheBlock) CacheResult {
	type pos struct {
		hash uint64
		cum  int
	}
	var ps []pos
	var breaks []int
	h := fnv.New64a()
	cum := 0
	lastGroup := ""
	for i, b := range blocks {
		h.Write([]byte(b.Key))
		h.Write([]byte{0})
		cum += b.Tokens
		merged := b.Group != "" && b.Group == lastGroup && i+1 < len(blocks) && blocks[i+1].Group == b.Group
		lastGroup = b.Group
		if merged && !b.Break {
			continue
		}
		ps = append(ps, pos{h.Sum64(), cum})
		if b.Break {
			breaks = append(breaks, len(ps)-1)
		}
	}
	total := cum
	if len(breaks) == 0 {
		return CacheResult{Uncached: total}
	}
	hit := -1
	for _, bp := range breaks {
		for k := bp; k >= 0 && k > bp-c.Lookback; k-- {
			if exp, ok := c.entries[ps[k].hash]; ok && now.Before(exp) {
				if k > hit {
					hit = k
				}
				break
			}
		}
	}
	read := 0
	if hit >= 0 {
		read = ps[hit].cum
		c.entries[ps[hit].hash] = now.Add(c.TTL)
	}
	last := breaks[len(breaks)-1]
	write := 0
	for _, bp := range breaks {
		if bp > hit && ps[bp].cum >= c.MinTokens {
			c.entries[ps[bp].hash] = now.Add(c.TTL)
		}
	}
	if last > hit && ps[last].cum >= c.MinTokens {
		write = ps[last].cum - read
	}
	return CacheResult{Read: read, Write: write, Uncached: total - read - write}
}

type Prices struct {
	Name     string  `json:"name"`
	Input    float64 `json:"input_per_mtok"`
	Write5m  float64 `json:"cache_write_5m_per_mtok"`
	Read     float64 `json:"cache_read_per_mtok"`
	Output   float64 `json:"output_per_mtok"`
	MinCache int     `json:"min_cacheable_tokens"`
}

func DefaultPrices() Prices {
	return Prices{Name: "Sonnet 5.5 input-side list prices from the prompt caching docs (fetched 2026-11-26); output tokens are reported, not priced", Input: 2.0, Write5m: 2.5, Read: 0.10, Output: 0, MinCache: 512}
}

func (p Prices) Cost(r CacheResult, output int) float64 {
	return (float64(r.Uncached)*p.Input + float64(r.Write)*p.Write5m + float64(r.Read)*p.Read + float64(output)*p.Output) / 1e6
}
