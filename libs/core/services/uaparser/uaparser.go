// Package uaparser ports ntx-packages/libs/core/src/services/ua-parser.service.ts.
// UAParserService parses User-Agent strings into structured browser/os/engine/
// device/cpu fields. The TS service wraps ua-parser-js v2 and stringifies each
// result group with `Object.values(obj).join('/')`, so the FIELD ORDER and the
// FIELD COUNT of every group are load-bearing:
//
//	browser → name/version/major/type   (4 slots)
//	cpu     → architecture              (1 slot)
//	device  → type/model/vendor         (3 slots)
//	engine  → name/version              (2 slots)
//	os      → name/version              (2 slots)
//
// ua-parser-js initialises every group with all keys present (undefined when not
// detected), and `join('/')` turns undefined into "". So an unrecognised UA does
// NOT yield an empty string — it yields the empty-slot skeleton: browser "///",
// os "/", engine "/", device "//", cpu "". We reproduce that exactly by always
// returning fixed-length slices.
//
// NOTE: ua-parser-js ships a large device model/vendor database we cannot fully
// reproduce by hand. The structural shape, OS/browser/engine naming, Windows
// version mapping and the common Apple/Android device cases match exactly; the
// Android model/vendor string remains a best-effort approximation for devices
// outside the common majors.
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

// Parse extracts browser/os/engine/device/cpu from ua. Mirrors the TS service:
// it does not short-circuit an empty UA — ua-parser-js still emits the empty-slot
// skeleton (browser "///", os "/", engine "/", device "//", cpu "").
func (s *Service) Parse(ua string) Result {
	return Result{
		Browser: strings.Join(detectBrowser(ua), "/"),
		OS:      strings.Join(detectOS(ua), "/"),
		Engine:  strings.Join(detectEngine(ua), "/"),
		Device:  strings.Join(detectDevice(ua), "/"),
		CPU:     strings.Join(detectCPU(ua), "/"),
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
	{"Mobile Chrome", regexp.MustCompile(`CriOS/([\d.]+)`)},
	{"Mobile Firefox", regexp.MustCompile(`FxiOS/([\d.]+)`)},
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

// detectBrowser returns [name, version, major, type]. Mirrors ua-parser-js
// getBrowser() {name, version, major, type}. type is empty for normal browsers
// (ua-parser-js only sets it to "inapp"/etc. for in-app webviews). ua-parser-js
// prefixes "Mobile " to Chrome/Firefox on mobile UAs (e.g. "Mobile Chrome").
func detectBrowser(ua string) []string {
	out := []string{"", "", "", ""}
	for _, r := range browserRules {
		if m := r.re.FindStringSubmatch(ua); len(m) > 1 {
			name := r.name
			if (name == "Chrome" || name == "Firefox") && strings.Contains(ua, "Mobile") {
				name = "Mobile " + name
			}
			out[0] = name
			out[1] = m[1]
			out[2] = majorVersion(m[1])
			return out
		}
	}
	return out
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

// windowsVersionMap maps the captured "Windows NT <x>" version to the marketing
// name ua-parser-js reports (windowsVersionMap in ua-parser.js).
var windowsVersionMap = map[string]string{
	"4.90": "ME",
	"3.51": "NT 3.51",
	"4.0":  "NT 4.0",
	"5.0":  "2000",
	"5.01": "2000",
	"5.1":  "XP",
	"5.2":  "XP",
	"6.0":  "Vista",
	"6.1":  "7",
	"6.2":  "8",
	"6.3":  "8.1",
	"6.4":  "10",
	"10.0": "10",
}

// detectOS returns [name, version]. Mirrors ua-parser-js getOS() {name, version}.
// Apple underscores are normalised to dots; the Windows NT number is mapped to
// its marketing name (NT 10.0 → "10", 6.1 → "7", 6.3 → "8.1", …).
func detectOS(ua string) []string {
	out := []string{"", ""}
	switch {
	case strings.Contains(ua, "Windows NT"):
		out[0] = "Windows"
		v := regexpFind(`Windows NT ([\d.]+)`, ua)
		if name, ok := windowsVersionMap[v]; ok {
			out[1] = name
		} else {
			out[1] = v
		}
	// iPhone OS UAs also contain "Mac OS X" (Apple compat token) — check iOS
	// before Mac OS so "iPhone; CPU iPhone OS 17_5_1 like Mac OS X" classifies
	// as iOS/17.5.1, not Mac OS.
	case strings.Contains(ua, "iPhone OS") || strings.Contains(ua, "iPad") || strings.Contains(ua, "iPod"):
		out[0] = "iOS"
		out[1] = strings.ReplaceAll(regexpFind(`OS ([\d_]+) like Mac OS`, ua), "_", ".")
	case strings.Contains(ua, "Mac OS X"):
		// ua-parser-js v2 reports "macOS" (not "Mac OS") and normalises
		// underscores to dots.
		out[0] = "macOS"
		out[1] = strings.ReplaceAll(regexpFind(`Mac OS X ([\d_\.]+)`, ua), "_", ".")
	case strings.Contains(ua, "CrOS"):
		out[0] = "Chrome OS"
		out[1] = regexpFind(`CrOS [^ ]+ ([\d.]+)`, ua)
	case strings.Contains(ua, "Android"):
		out[0] = "Android"
		out[1] = regexpFind(`Android ([\d.]+)`, ua)
	case strings.Contains(ua, "Ubuntu"):
		out[0] = "Ubuntu"
		out[1] = regexpFind(`Ubuntu/([\d.]+)`, ua)
	case strings.Contains(ua, "Fedora"):
		out[0] = "Fedora"
	case strings.Contains(ua, "Linux"):
		out[0] = "Linux"
	case strings.Contains(ua, "FreeBSD"):
		out[0] = "FreeBSD"
	}
	return out
}

// ── engine ─────────────────────────────────────────────────────────────────────

// detectEngine returns [name, version]. Mirrors ua-parser-js getEngine(). For
// Chromium-based browsers the engine is "Blink" and its version is the CHROME
// version (not the AppleWebKit version), matching ua-parser-js.
func detectEngine(ua string) []string {
	out := []string{"", ""}
	switch {
	case strings.Contains(ua, "Trident"):
		return []string{"Trident", regexpFind(`Trident/([\d.]+)`, ua)}
	case strings.Contains(ua, "EdgeHTML"):
		return []string{"EdgeHTML", regexpFind(`EdgeHTML/([\d.]+)`, ua)}
	case strings.Contains(ua, "Gecko") &&
		(strings.Contains(ua, "Firefox") || strings.Contains(ua, "Seamonkey") || strings.Contains(ua, "Camino")):
		return []string{"Gecko", regexpFind(`rv:([\d.]+)`, ua)}
	case strings.Contains(ua, "AppleWebKit"):
		// Modern Blink browsers still report AppleWebKit/537.36; ua-parser-js
		// classifies them as Blink with the Chrome version.
		if strings.Contains(ua, "Chrome/") || strings.Contains(ua, "Edg/") || strings.Contains(ua, "OPR/") {
			return []string{"Blink", regexpFind(`Chrome/([\d.]+)`, ua)}
		}
		return []string{"WebKit", regexpFind(`AppleWebKit/([\d.]+)`, ua)}
	case strings.Contains(ua, "Presto"):
		return []string{"Presto", regexpFind(`Presto/([\d.]+)`, ua)}
	}
	return out
}

// ── device ─────────────────────────────────────────────────────────────────────

// detectDevice returns [type, model, vendor]. Mirrors ua-parser-js getDevice()
// {type, model, vendor}. A desktop UA leaves all three undefined → "//"; macOS
// desktops are special — ua-parser-js reports {model:"Macintosh", vendor:"Apple"}
// with an undefined type → "/Macintosh/Apple".
func detectDevice(ua string) []string {
	switch {
	case strings.Contains(ua, "iPhone"):
		return []string{"mobile", "iPhone", "Apple"}
	case strings.Contains(ua, "iPad"):
		return []string{"tablet", "iPad", "Apple"}
	case strings.Contains(ua, "iPod"):
		return []string{"mobile", "iPod", "Apple"}
	case strings.Contains(ua, "Android"):
		model := extractAndroidModel(ua)
		typ := "tablet"
		if strings.Contains(ua, "Mobile") {
			typ = "mobile"
		}
		return []string{typ, model, guessAndroidVendor(model)}
	case strings.Contains(ua, "Windows Phone"):
		return []string{"mobile", "Windows Phone", "Microsoft"}
	case strings.Contains(ua, "BlackBerry") || strings.Contains(ua, "BB10"):
		return []string{"mobile", "BlackBerry", "BlackBerry"}
	case strings.Contains(ua, "Macintosh"):
		// ua-parser-js: {type:undefined, model:"Macintosh", vendor:"Apple"}.
		return []string{"", "Macintosh", "Apple"}
	}
	return []string{"", "", ""}
}

// extractAndroidModel pulls the model token out of "(Linux; Android X; <model>)".
// ua-parser-js strips a "Build/..." suffix and a leading "SAMSUNG " manufacturer
// token, and treats the bare "Mobile" form-factor token as no model.
func extractAndroidModel(ua string) string {
	model := strings.TrimSpace(regexpFind(`Android [^;]+;\s*([^;)]+)[;)]`, ua))
	if idx := strings.Index(model, " Build/"); idx >= 0 {
		model = model[:idx]
	}
	model = strings.TrimSpace(strings.TrimPrefix(model, "SAMSUNG "))
	if model == "Mobile" || strings.HasPrefix(model, "rv:") {
		return ""
	}
	return model
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

// detectCPU returns [architecture]. Mirrors ua-parser-js getCPU() {architecture}.
// Note ua-parser-js does NOT infer amd64 from a bare "Intel Mac OS X" token, so
// neither do we — a Mac Safari UA yields an empty architecture.
func detectCPU(ua string) []string {
	arch := ""
	switch {
	// arm64 must precede arm so "arm64" doesn't get classified as plain arm.
	case strings.Contains(ua, "arm64") || strings.Contains(ua, "ARM64") || strings.Contains(ua, "aarch64"):
		arch = "arm64"
	case strings.Contains(ua, "x86_64") || strings.Contains(ua, "x64") || strings.Contains(ua, "Win64") || strings.Contains(ua, "WOW64"):
		arch = "amd64"
	case strings.Contains(ua, "armv") || strings.Contains(ua, "arm;") || strings.Contains(ua, "ARM"):
		arch = "arm"
	case strings.Contains(ua, "i686") || strings.Contains(ua, "i386") || strings.Contains(ua, "ia32"):
		arch = "ia32"
	}
	return []string{arch}
}

// ── helpers ────────────────────────────────────────────────────────────────────

func regexpFind(pattern, ua string) string {
	re := regexp.MustCompile(pattern)
	m := re.FindStringSubmatch(ua)
	if len(m) > 1 {
		return m[1]
	}
	return ""
}
