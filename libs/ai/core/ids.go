package core

import (
	"crypto/rand"
	"encoding/hex"
)

// NewID returns prefix_ followed by 24 random hex characters, e.g. "run_3f9a…".
// It panics if the system's random source fails, which leaves nothing safe to
// do: an id that is predictable or repeated would break idempotency keys.
func NewID(prefix string) string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("core: random source failed: " + err.Error())
	}
	if prefix == "" {
		return hex.EncodeToString(b[:])
	}
	return prefix + "_" + hex.EncodeToString(b[:])
}
