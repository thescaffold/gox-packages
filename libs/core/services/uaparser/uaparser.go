// Package uaparser ports ntx-packages/libs/core/src/services/ua-parser.service.ts.
// UAParserService parses User-Agent strings into structured browser/os/engine/device/cpu fields.
package uaparser

import (
	"regexp"
	"strings"
)

// Result mirrors TS UAParserService.parse() return shape.
type Result struct {
	Browser string `json:"browser"`
	OS      string `json:"os"`
	Engine  string `json:"engine"`
	Device  string `json:"device"`
	CPU     string `json:"cpu"`
}

// Service parses UA strings into Result. Stateless.
type Service struct{}

// New constructs a UA Parser service.
func New() *Service { return &Service{} }

// Parse extracts browser/os/engine/device/cpu from ua. Empty values are
// reported as "" (matching TS stringify(obj) which joins values with "/").
func (s *Service) Parse(ua string) Result {
	if ua == "" {
		return Result{}
	}
	return Result{
		Browser: detectBrowser(ua),
		OS:      detectOS(ua),
		Engine:  detectEngine(ua),
		Device:  detectDevice(ua),
		CPU:     detectCPU(ua),
	}
}

// detectBrowser uses regex match on common browser tokens.
func detectBrowser(ua string) string {
	rules := []struct {
		name string
		re   *regexp.Regexp
	}{
		{"Edge", regexp.MustCompile(`Edg(?:e|A|iOS)?/([\d.]+)`)},
		{"Chrome", regexp.MustCompile(`Chrome/([\d.]+)`)},
		{"Firefox", regexp.MustCompile(`Firefox/([\d.]+)`)},
		{"Safari", regexp.MustCompile(`Version/([\d.]+).*Safari`)},
		{"Opera", regexp.MustCompile(`OPR/([\d.]+)`)},
		{"IE", regexp.MustCompile(`MSIE ([\d.]+)`)},
	}
	for _, r := range rules {
		if m := r.re.FindStringSubmatch(ua); len(m) > 1 {
			return r.name + "/" + m[1]
		}
	}
	return ""
}

func detectOS(ua string) string {
	switch {
	case strings.Contains(ua, "Windows NT"):
		return stringify("Windows", regexpFind(`Windows NT ([\d.]+)`, ua))
	case strings.Contains(ua, "Mac OS X"):
		return stringify("macOS", regexpFind(`Mac OS X ([\d_\.]+)`, ua))
	case strings.Contains(ua, "Android"):
		return stringify("Android", regexpFind(`Android ([\d.]+)`, ua))
	case strings.Contains(ua, "iPhone OS") || strings.Contains(ua, "iPad"):
		return stringify("iOS", regexpFind(`OS ([\d_]+) like Mac OS`, ua))
	case strings.Contains(ua, "Linux"):
		return "Linux"
	}
	return ""
}

func detectEngine(ua string) string {
	switch {
	case strings.Contains(ua, "AppleWebKit"):
		return stringify("WebKit", regexpFind(`AppleWebKit/([\d.]+)`, ua))
	case strings.Contains(ua, "Gecko"):
		return stringify("Gecko", regexpFind(`Gecko/([\d.]+)`, ua))
	case strings.Contains(ua, "Trident"):
		return "Trident"
	}
	return ""
}

func detectDevice(ua string) string {
	switch {
	case strings.Contains(ua, "iPhone"):
		return "iPhone"
	case strings.Contains(ua, "iPad"):
		return "iPad"
	case strings.Contains(ua, "Android") && strings.Contains(ua, "Mobile"):
		return "Mobile"
	case strings.Contains(ua, "Android"):
		return "Tablet"
	}
	return ""
}

func detectCPU(ua string) string {
	switch {
	case strings.Contains(ua, "x86_64") || strings.Contains(ua, "x64"):
		return "amd64"
	case strings.Contains(ua, "arm64") || strings.Contains(ua, "ARM64"):
		return "arm64"
	case strings.Contains(ua, "i686") || strings.Contains(ua, "i386"):
		return "x86"
	}
	return ""
}

func stringify(parts ...string) string {
	out := ""
	for i, p := range parts {
		if p == "" {
			continue
		}
		if i > 0 && out != "" {
			out += "/"
		}
		out += p
	}
	return out
}

func regexpFind(pattern, ua string) string {
	re := regexp.MustCompile(pattern)
	m := re.FindStringSubmatch(ua)
	if len(m) > 1 {
		return m[1]
	}
	return ""
}
