// Package uaparser ports ntx-packages/libs/core/src/services/ua-parser.service.ts.
// UAParserService parses User-Agent strings into structured browser/os/engine/
// device/cpu fields. Mirrors the ua-parser-js library that the TS service wraps,
// with the same `Object.values(obj).join('/')` stringify semantics that the
// downstream identity providers depend on.
package uaparser

import (
	"regexp"
	"strings"
)

// Result mirrors TS UAParserService.parse() return shape — every field is the
// `Object.values(getXxx()).join('/')` joined string for that ua-parser group.
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

// Parse extracts browser/os/engine/device/cpu from ua. An empty UA yields the
// zero-value Result (all empty strings), matching TS which returns null fields
// when ua-parser-js can't classify the input — the gox stringify uses "" in
// that slot rather than the JS string "null" so downstream null-checks work.
func (s *Service) Parse(ua string) Result {
	if ua == "" {
		return Result{}
	}
	return Result{
		Browser: stringify(detectBrowser(ua)),
		OS:      stringify(detectOS(ua)),
		Engine:  stringify(detectEngine(ua)),
		Device:  stringify(detectDevice(ua)),
		CPU:     stringify(detectCPU(ua)),
	}
}

// ── browser ────────────────────────────────────────────────────────────────────

// browserRule is one regex + name combo. The capture group is the version.
type browserRule struct {
	name string
	re   *regexp.Regexp
}

// Order matters: rules higher up win on overlap. Edge ships UA strings that
// also contain "Chrome", so Edge must precede Chrome; the same applies to
// Opera (also contains "Chrome").
var browserRules = []browserRule{
	{"Edge", regexp.MustCompile(`Edg(?:e|A|iOS)?/([\d.]+)`)},
	{"Opera", regexp.MustCompile(`OPR/([\d.]+)`)},
	{"Opera", regexp.MustCompile(`Opera/([\d.]+)`)},
	{"Chrome Mobile", regexp.MustCompile(`CriOS/([\d.]+)`)},
	{"Firefox", regexp.MustCompile(`FxiOS/([\d.]+)`)},
	{"Chromium", regexp.MustCompile(`Chromium/([\d.]+)`)},
	{"Samsung Internet", regexp.MustCompile(`SamsungBrowser/([\d.]+)`)},
	{"Chrome", regexp.MustCompile(`Chrome/([\d.]+)`)},
	{"Firefox", regexp.MustCompile(`Firefox/([\d.]+)`)},
	{"Mobile Safari", regexp.MustCompile(`Version/([\d.]+).*Mobile.*Safari`)},
	{"Safari", regexp.MustCompile(`Version/([\d.]+).*Safari`)},
	{"IE", regexp.MustCompile(`MSIE ([\d.]+)`)},
	// IE 11 dropped the "MSIE" token and only carries Trident + rv:11.0; in the
	// canonical UA Trident appears BEFORE rv:, so accept either order.
	{"IE", regexp.MustCompile(`Trident/[\d.]+.*rv:([\d.]+)`)},
	{"IE", regexp.MustCompile(`rv:([\d.]+).*Trident`)},
}

// detectBrowser returns [name, version, major]. Mirrors ua-parser-js's
// getBrowser() which exposes {name, version, major}. Major is the leading
// integer of version.
func detectBrowser(ua string) []string {
	for _, r := range browserRules {
		if m := r.re.FindStringSubmatch(ua); len(m) > 1 {
			return []string{r.name, m[1], majorVersion(m[1])}
		}
	}
	return nil
}

// majorVersion returns the leading integer portion of "X.Y.Z" — e.g. "120" for
// "120.0.6099.225". Empty input yields empty.
func majorVersion(version string) string {
	if version == "" {
		return ""
	}
	if i := strings.IndexByte(version, '.'); i >= 0 {
		return version[:i]
	}
	return version
}

// ── os ─────────────────────────────────────────────────────────────────────────

// detectOS returns [name, version]. Mirrors ua-parser-js getOS()'s {name,version}.
// version is normalized to dotted form (TS-style) even when the UA used
// underscores (the Apple convention).
func detectOS(ua string) []string {
	switch {
	case strings.Contains(ua, "Windows NT"):
		return []string{"Windows", regexpFind(`Windows NT ([\d.]+)`, ua)}
	// iPhone OS UAs also contain "Mac OS X" (Apple compat token) — check iOS
	// before Mac OS so "iPhone; CPU iPhone OS 17_5_1 like Mac OS X" classifies
	// as iOS/17.5.1, not Mac OS.
	case strings.Contains(ua, "iPhone OS") || strings.Contains(ua, "iPad") || strings.Contains(ua, "iPod"):
		v := regexpFind(`OS ([\d_]+) like Mac OS`, ua)
		return []string{"iOS", strings.ReplaceAll(v, "_", ".")}
	case strings.Contains(ua, "Mac OS X"):
		v := regexpFind(`Mac OS X ([\d_\.]+)`, ua)
		// Apple writes underscores; ua-parser-js normalizes to dots.
		return []string{"Mac OS", strings.ReplaceAll(v, "_", ".")}
	case strings.Contains(ua, "CrOS"):
		return []string{"Chromium OS", regexpFind(`CrOS [^ ]+ ([\d.]+)`, ua)}
	case strings.Contains(ua, "Android"):
		return []string{"Android", regexpFind(`Android ([\d.]+)`, ua)}
	case strings.Contains(ua, "Ubuntu"):
		return []string{"Ubuntu", regexpFind(`Ubuntu/([\d.]+)`, ua)}
	case strings.Contains(ua, "Fedora"):
		return []string{"Fedora", ""}
	case strings.Contains(ua, "Linux"):
		return []string{"Linux", ""}
	case strings.Contains(ua, "FreeBSD"):
		return []string{"FreeBSD", ""}
	}
	return nil
}

// ── engine ─────────────────────────────────────────────────────────────────────

// detectEngine returns [name, version]. Mirrors ua-parser-js getEngine().
// Blink is reported separately from WebKit when present — matches ua-parser-js
// modern behaviour for Chromium-based browsers.
func detectEngine(ua string) []string {
	switch {
	case strings.Contains(ua, "Trident"):
		return []string{"Trident", regexpFind(`Trident/([\d.]+)`, ua)}
	case strings.Contains(ua, "EdgeHTML"):
		return []string{"EdgeHTML", regexpFind(`EdgeHTML/([\d.]+)`, ua)}
	case strings.Contains(ua, "Gecko"):
		// Firefox uses Gecko. AppleWebKit-only UAs also mention Gecko in their
		// compat token, so Gecko-only matches need a Firefox/Camino marker.
		if strings.Contains(ua, "Firefox") || strings.Contains(ua, "Seamonkey") || strings.Contains(ua, "Camino") {
			return []string{"Gecko", regexpFind(`rv:([\d.]+)`, ua)}
		}
		fallthrough
	case strings.Contains(ua, "AppleWebKit"):
		// Modern Blink-based browsers still report AppleWebKit/537.36; classify
		// them as Blink to match ua-parser-js when Chrome/Edg/OPR token is
		// present.
		webkit := regexpFind(`AppleWebKit/([\d.]+)`, ua)
		if strings.Contains(ua, "Chrome/") || strings.Contains(ua, "Edg/") || strings.Contains(ua, "OPR/") {
			return []string{"Blink", webkit}
		}
		return []string{"WebKit", webkit}
	case strings.Contains(ua, "Presto"):
		return []string{"Presto", regexpFind(`Presto/([\d.]+)`, ua)}
	}
	return nil
}

// ── device ─────────────────────────────────────────────────────────────────────

// detectDevice returns [model, type, vendor]. Mirrors ua-parser-js getDevice()'s
// {model, type, vendor}. Desktop browsers report only "/// " (all fields empty)
// because ua-parser-js leaves the device object empty when the UA does not
// include a recognizable device hint.
func detectDevice(ua string) []string {
	switch {
	case strings.Contains(ua, "iPhone"):
		return []string{"iPhone", "mobile", "Apple"}
	case strings.Contains(ua, "iPad"):
		return []string{"iPad", "tablet", "Apple"}
	case strings.Contains(ua, "iPod"):
		return []string{"iPod", "mobile", "Apple"}
	case strings.Contains(ua, "Mac OS X"):
		// macOS desktops have no model — but ua-parser-js still leaves the
		// device object empty. Return nil to match.
		return nil
	case strings.Contains(ua, "Android"):
		// Try to extract model from "(Linux; Android X; <model>)".
		model := regexpFind(`Android [^;]+; ([^)]+)\)`, ua)
		// Strip trailing build identifier ("Build/...").
		if idx := strings.Index(model, " Build/"); idx >= 0 {
			model = model[:idx]
		}
		typ := "tablet"
		if strings.Contains(ua, "Mobile") {
			typ = "mobile"
		}
		vendor := guessAndroidVendor(model)
		return []string{strings.TrimSpace(model), typ, vendor}
	case strings.Contains(ua, "Windows Phone"):
		return []string{"Windows Phone", "mobile", "Microsoft"}
	case strings.Contains(ua, "BlackBerry") || strings.Contains(ua, "BB10"):
		return []string{"BlackBerry", "mobile", "BlackBerry"}
	}
	return nil
}

// guessAndroidVendor maps a model prefix to a likely vendor. ua-parser-js
// ships a database for this; the rules here cover the common majors.
func guessAndroidVendor(model string) string {
	m := strings.ToLower(model)
	switch {
	case strings.HasPrefix(m, "sm-"), strings.HasPrefix(m, "gt-"), strings.HasPrefix(m, "samsung"):
		return "Samsung"
	case strings.HasPrefix(m, "pixel"):
		return "Google"
	case strings.HasPrefix(m, "huawei"):
		return "Huawei"
	case strings.HasPrefix(m, "xiaomi"), strings.HasPrefix(m, "mi "):
		return "Xiaomi"
	case strings.HasPrefix(m, "redmi"):
		return "Xiaomi"
	case strings.HasPrefix(m, "lg-"):
		return "LG"
	case strings.HasPrefix(m, "moto"), strings.HasPrefix(m, "xt"):
		return "Motorola"
	case strings.HasPrefix(m, "oneplus"):
		return "OnePlus"
	case strings.HasPrefix(m, "nexus"):
		return "Google"
	}
	return ""
}

// ── cpu ────────────────────────────────────────────────────────────────────────

// detectCPU returns [architecture]. Mirrors ua-parser-js getCPU(): {architecture}.
// Naming follows ua-parser-js conventions ("amd64", "ia32", "arm64", …).
func detectCPU(ua string) []string {
	switch {
	// arm64 must precede arm so "arm64" doesn't get classified as plain arm.
	case strings.Contains(ua, "arm64") || strings.Contains(ua, "ARM64") || strings.Contains(ua, "aarch64"):
		return []string{"arm64"}
	case strings.Contains(ua, "x86_64") || strings.Contains(ua, "x64") || strings.Contains(ua, "Win64") || strings.Contains(ua, "WOW64"):
		return []string{"amd64"}
	// macOS UAs say "Intel Mac OS X" rather than carrying an explicit x86_64
	// token; ua-parser-js treats Intel-on-Mac as amd64 for parity with how the
	// platform reports itself.
	case strings.Contains(ua, "Intel Mac OS X"):
		return []string{"amd64"}
	case strings.Contains(ua, "armv") || strings.Contains(ua, "arm;") || strings.Contains(ua, "ARM"):
		return []string{"arm"}
	case strings.Contains(ua, "i686") || strings.Contains(ua, "i386") || strings.Contains(ua, "ia32"):
		return []string{"ia32"}
	}
	return nil
}

// ── helpers ────────────────────────────────────────────────────────────────────

// stringify mirrors TS `Object.values(obj).join('/')`. nil yields an empty
// string; empty slots produce a trailing or interior slash, exactly as the JS
// behaviour for undefined values that become the literal string "undefined" —
// gox uses "" for undefined to keep downstream null-checks meaningful.
func stringify(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "/")
}

func regexpFind(pattern, ua string) string {
	re := regexp.MustCompile(pattern)
	m := re.FindStringSubmatch(ua)
	if len(m) > 1 {
		return m[1]
	}
	return ""
}
