package core

import "math"

// Usage is the token accounting of one model call.
type Usage struct {
	InputTokens      int64 `json:"inputTokens"`
	OutputTokens     int64 `json:"outputTokens"`
	CacheReadTokens  int64 `json:"cacheReadTokens,omitempty"`
	CacheWriteTokens int64 `json:"cacheWriteTokens,omitempty"`
}

// Add returns the sum of two usages (accumulating a run's total).
func (u Usage) Add(o Usage) Usage {
	return Usage{
		InputTokens:      u.InputTokens + o.InputTokens,
		OutputTokens:     u.OutputTokens + o.OutputTokens,
		CacheReadTokens:  u.CacheReadTokens + o.CacheReadTokens,
		CacheWriteTokens: u.CacheWriteTokens + o.CacheWriteTokens,
	}
}

// CacheHitRate is cache-read tokens as a share of every input-side token
// (fresh + cache read + cache write), or 0 when there was no input. A rate that
// falls below 60% for a role usually means something is silently invalidating
// the cache (TRD §6.2).
func (u Usage) CacheHitRate() float64 {
	total := u.InputTokens + u.CacheReadTokens + u.CacheWriteTokens
	if total == 0 {
		return 0
	}
	return float64(u.CacheReadTokens) / float64(total)
}

// Pricing is a model's price in micro-USD per million tokens, so $1 per MTok
// is 1_000_000. Money here is always integer micro-USD (1e-6 USD), never a
// float: $1 per MTok is exactly 1 micro-USD per token.
type Pricing struct {
	Model             string `json:"model"`
	InputPerMTok      int64  `json:"inputPerMTok"`
	OutputPerMTok     int64  `json:"outputPerMTok"`
	CacheReadPerMTok  int64  `json:"cacheReadPerMTok,omitempty"`
	CacheWritePerMTok int64  `json:"cacheWritePerMTok,omitempty"`
}

// MicroUSD is an amount of money in millionths of a US dollar.
type MicroUSD int64

// Cost prices a usage. The sum is rounded UP to the next micro-USD once, so a
// call is never under-charged and a run of tiny calls does not round each one
// to zero. It saturates rather than wrapping on overflow.
func Cost(u Usage, p Pricing) MicroUSD {
	terms := [][2]int64{
		{u.InputTokens, p.InputPerMTok},
		{u.OutputTokens, p.OutputPerMTok},
		{u.CacheReadTokens, p.CacheReadPerMTok},
		{u.CacheWriteTokens, p.CacheWritePerMTok},
	}
	var exact int64
	overflow := false
	for _, t := range terms {
		if t[0] < 0 || t[1] < 0 {
			continue
		}
		if t[1] != 0 && t[0] > math.MaxInt64/t[1] {
			overflow = true
			break
		}
		prod := t[0] * t[1]
		if exact > math.MaxInt64-prod {
			overflow = true
			break
		}
		exact += prod
	}
	if overflow {
		return MicroUSD(math.MaxInt64)
	}
	q := exact / 1_000_000
	if exact%1_000_000 != 0 {
		q++
	}
	return MicroUSD(q)
}
