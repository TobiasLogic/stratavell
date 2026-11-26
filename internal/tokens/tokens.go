package tokens

import (
	"math"
	"sync"
	"unicode/utf8"
)

const MessageOverhead = 6

func Estimate(s string) int {
	if len(s) == 0 {
		return 0
	}
	n := 0
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case isLetter(c):
			j := i
			for j < len(s) && isLetter(s[j]) {
				j++
			}
			w := j - i
			n += 1 + (w-1)/5
			i = j
		case c >= '0' && c <= '9':
			j := i
			for j < len(s) && s[j] >= '0' && s[j] <= '9' {
				j++
			}
			n += (j - i + 2) / 3
			i = j
		case c == ' ':
			j := i
			for j < len(s) && s[j] == ' ' {
				j++
			}
			if j-i > 1 {
				n += (j - i) / 4
			}
			i = j
		case c == '\n' || c == '\t' || c == '\r':
			n++
			i++
		case c < utf8.RuneSelf:
			n++
			i++
		default:
			_, size := utf8.DecodeRuneInString(s[i:])
			if size >= 3 {
				n += 2
			} else {
				n++
			}
			i += size
		}
	}
	return n
}

func isLetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_'
}

type Calibrator struct {
	mu      sync.Mutex
	ratio   float64
	samples int
	min     float64
	max     float64
}

func NewCalibrator() *Calibrator { return &Calibrator{ratio: 1, min: 0.6, max: 1.8} }

func (c *Calibrator) Observe(estimated, actual int) {
	if estimated <= 0 || actual <= 0 {
		return
	}
	r := float64(actual) / float64(estimated)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.samples == 0 {
		c.ratio = r
	} else if r > c.ratio {
		c.ratio = 0.5*c.ratio + 0.5*r
	} else {
		c.ratio = 0.9*c.ratio + 0.1*r
	}
	c.ratio = math.Max(c.min, math.Min(c.max, c.ratio))
	c.samples++
}

func (c *Calibrator) Ratio() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.samples == 0 {
		return 1
	}
	return c.ratio
}

func (c *Calibrator) Samples() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.samples
}

func (c *Calibrator) Adjust(n int) int {
	return int(math.Ceil(float64(n) * math.Max(1, c.Ratio())))
}
