package dns

import "time"

// TTLPolicy computes per-answer and negative TTLs from lease remaining time.
type TTLPolicy struct {
	MaxTTL     time.Duration
	NegativeTTL time.Duration
}

// AnswerTTL returns min(maxTTL, remaining lease), floored at 1 second when any lease remains.
func (p TTLPolicy) AnswerTTL(expiresAt, now time.Time) uint32 {
	maxSec := uint32(p.MaxTTL / time.Second)
	if maxSec < 1 {
		maxSec = 1
	}
	if expiresAt.IsZero() {
		return maxSec
	}
	remaining := expiresAt.Sub(now)
	if remaining <= 0 {
		return 1
	}
	remSec := uint32(remaining / time.Second)
	if remSec < 1 {
		remSec = 1
	}
	if remSec < maxSec {
		return remSec
	}
	return maxSec
}

// NegTTL returns the configured negative-caching TTL (default 2s).
func (p TTLPolicy) NegTTL() uint32 {
	sec := uint32(p.NegativeTTL / time.Second)
	if sec < 1 {
		return 1
	}
	return sec
}
