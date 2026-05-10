// Package otp ports ntx-packages/libs/core/src/services/otp.service.ts.
// OtpService creates and verifies short-lived OTP codes via a CacheBackend.
package otp

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/thescaffold/gox-packages/libs/core/security"
	"github.com/thescaffold/gox-packages/libs/core/services"
	"github.com/thescaffold/gox-packages/libs/core/utils"
)

// OTP is the persisted record. Mirrors the TS otp object shape.
type OTP struct {
	Value      string `json:"value"`
	Owner      string `json:"owner"`
	Type       string `json:"type"`
	UserID     string `json:"userId"`
	Hashed     string `json:"hashed"`
	Source     string `json:"source"`
	IsRegister bool   `json:"isRegister"`
	IsReset    bool   `json:"isReset"`
}

// Service generates and verifies OTPs.
type Service struct {
	cache  services.CacheBackend
	expiry time.Duration
}

// New constructs an OtpService. expiry defaults to 7 minutes when zero.
func New(cache services.CacheBackend, expiry time.Duration) *Service {
	if expiry <= 0 {
		expiry = 7 * time.Minute
	}
	return &Service{cache: cache, expiry: expiry}
}

// Initiate creates (or returns the existing) OTP for the given user/owner pair.
// Mirrors TS OtpService.initiate(). The sendEvent flag is the caller's
// responsibility — Go doesn't dispatch a notification message here; pair this
// with a notification publisher if needed.
func (s *Service) Initiate(otpType, userID, owner, source string) *OTP {
	key := fmt.Sprintf("ntx:identity:%s:%s:%s:%s", source, otpType, userID, owner)

	if existing, ok := s.cache.Get(key); ok {
		var out OTP
		if err := json.Unmarshal([]byte(existing), &out); err == nil {
			return &out
		}
	}

	value := utils.RandomDigits(3) + utils.RandomDigits(3)
	hashed := security.ToBase64(fmt.Sprintf(`{"value":%q,"owner":%q,"type":%q,"userId":%q}`,
		value, owner, otpType, userID))
	otp := OTP{
		Value:      value,
		Owner:      owner,
		Type:       otpType,
		UserID:     userID,
		Hashed:     hashed,
		Source:     source,
		IsRegister: source == "register",
		IsReset:    source == "reset",
	}
	bytes, _ := json.Marshal(otp)
	s.cache.Set(key, string(bytes), s.expiry)
	return &otp
}

// Verify compares value against the stored OTP. When clear is true, a matching
// OTP is deleted so it can't be reused. Returns the OTP record on success, or
// an error if missing/mismatched.
func (s *Service) Verify(value, otpType, userID, owner, source string, clear bool) (*OTP, error) {
	key := fmt.Sprintf("ntx:identity:%s:%s:%s:%s", source, otpType, userID, owner)
	existing, ok := s.cache.Get(key)
	if !ok {
		return nil, fmt.Errorf("otp not found")
	}
	var stored OTP
	if err := json.Unmarshal([]byte(existing), &stored); err != nil {
		return nil, fmt.Errorf("otp corrupted")
	}
	if stored.Value != value {
		return nil, fmt.Errorf("otp mismatch")
	}
	if clear {
		s.cache.Del(key)
	}
	return &stored, nil
}
