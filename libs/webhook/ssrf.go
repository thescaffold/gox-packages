package webhook

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"
)

// ErrBlockedAddress is returned when a delivery would connect to a private,
// loopback, link-local or otherwise non-public address.
var ErrBlockedAddress = errors.New("webhook: destination address is not allowed")

// blocked reports whether ip must never be a webhook destination.
func blocked(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified()
}

// NewSafeClient returns an http.Client for delivering to URLs that
// customers control. It refuses to connect to private, loopback and
// link-local addresses. The check runs in the dialer's Control hook, on the
// address actually being connected to, so it holds after DNS resolution
// (defeating DNS rebinding) and for every redirect hop. Redirects are not
// followed at all: a receiver answering 3xx is treated as a response.
func NewSafeClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{
		Timeout: 5 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip == nil || blocked(ip) {
				return fmt.Errorf("%w: %s", ErrBlockedAddress, host)
			}
			return nil
		},
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext:           dialer.DialContext,
			Proxy:                 nil, // an env proxy would bypass the dial-time check
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: timeout,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}
