package operations

import (
	"crypto/rand"
	"sync"
	"time"
)

const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// Generator produces op_<ULID> ids (48-bit ms timestamp + 80-bit randomness,
// Crockford Base32), matching Control's Ulid convention.
type Generator struct {
	mu              sync.Mutex
	lastTimestampMs int64
	lastRandom      [10]byte
}

// NewGenerator returns a monotonic ULID generator.
func NewGenerator() *Generator {
	return &Generator{}
}

// Next returns a new id with the given prefix (e.g. "op").
func (g *Generator) Next(prefix string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	body := g.nextBodyLocked()
	return prefix + "_" + body
}

// NewOpID returns op_<ULID>.
func (g *Generator) NewOpID() string {
	return g.Next("op")
}

func (g *Generator) nextBodyLocked() string {
	now := time.Now().UnixMilli()
	if now == g.lastTimestampMs {
		if !incrementRandom(&g.lastRandom) {
			for now == g.lastTimestampMs {
				now = time.Now().UnixMilli()
			}
			_, _ = rand.Read(g.lastRandom[:])
			g.lastTimestampMs = now
		}
	} else {
		_, _ = rand.Read(g.lastRandom[:])
		g.lastTimestampMs = now
	}
	return encode(g.lastTimestampMs, g.lastRandom[:])
}

func incrementRandom(bytes *[10]byte) bool {
	for i := len(bytes) - 1; i >= 0; i-- {
		next := int(bytes[i]) + 1
		bytes[i] = byte(next & 0xff)
		if next <= 0xff {
			return true
		}
	}
	return false
}

func encode(timestampMs int64, randomness []byte) string {
	chars := make([]byte, 26)
	ts := timestampMs & 0xFFFF_FFFF_FFFF
	for i := 9; i >= 0; i-- {
		chars[i] = alphabet[ts&0x1f]
		ts >>= 5
	}
	acc := 0
	bits := 0
	out := 10
	for _, b := range randomness {
		acc = (acc << 8) | int(b)
		bits += 8
		for bits >= 5 {
			bits -= 5
			chars[out] = alphabet[(acc>>bits)&0x1f]
			out++
		}
	}
	if bits > 0 {
		chars[out] = alphabet[(acc<<(5-bits))&0x1f]
	}
	return string(chars)
}
