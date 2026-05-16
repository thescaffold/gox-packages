package utils

import (
	"crypto/md5"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
)

// UUID returns a new random UUID v4 string.
func UUID() string {
	return uuid.New().String()
}

// Reference generates a service reference token matching TS common.util.ts
// reference(): `${service}-${host}-${processId}-${wildcard}-${anotherWildcard}-${time}`
// sliced to maxLength-1 and upper-cased, where:
//   - host       = md5(hostname) hex, first 6 chars
//   - processId  = process pid padded to at least 3 digits (NOT modulo 1000)
//   - wildcard   = 6 hex characters of crypto randomness
//   - time       = current epoch milliseconds
func Reference(service string, maxLen int) string {
	if service == "" {
		service = "NTX"
	}
	if maxLen <= 0 {
		maxLen = 36
	}

	host, _ := os.Hostname()
	hostHash := fmt.Sprintf("%x", md5.Sum([]byte(host)))[:6]
	pid := fmt.Sprintf("%03d", os.Getpid())
	r1 := Random(6)
	r2 := Random(6)
	ts := fmt.Sprintf("%d", time.Now().UnixMilli())

	ref := strings.ToUpper(
		service + "-" + hostHash + "-" + pid + "-" + r1 + "-" + r2 + "-" + ts,
	)
	if len(ref) >= maxLen {
		return ref[:maxLen-1]
	}
	return ref
}
