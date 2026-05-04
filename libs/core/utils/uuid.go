package utils

import (
	"crypto/md5"
	"fmt"
	"math/rand/v2"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
)

// UUID returns a new random UUID v4 string.
func UUID() string {
	return uuid.New().String()
}

// Reference generates a service reference token matching the TS implementation:
// {service}-{host_md5[:6]}-{pid:03d}-{rand[:6]}-{rand[:6]}-{timestamp}, upper-cased, truncated to maxLen.
func Reference(service string, maxLen int) string {
	if service == "" {
		service = "NTX"
	}
	if maxLen <= 0 {
		maxLen = 36
	}

	host, _ := os.Hostname()
	hostHash := fmt.Sprintf("%x", md5.Sum([]byte(host)))[:6]
	pid := fmt.Sprintf("%03d", os.Getpid()%1000)
	r1 := randomFrom(alphaNumChars, 8)[:6]
	r2 := randomFrom(alphaNumChars, 8)[:6]
	ts := fmt.Sprintf("%d", time.Now().UnixMilli())

	// prevent rand seed collision in rapid calls
	_ = rand.IntN(1)

	ref := strings.ToUpper(
		service + "-" + hostHash + "-" + pid + "-" + r1 + "-" + r2 + "-" + ts,
	)
	if len(ref) >= maxLen {
		return ref[:maxLen-1]
	}
	return ref
}
