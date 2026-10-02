package prototype

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
)

// Violation is one rule a prototype breaks.
type Violation struct {
	File    string `json:"file"`
	Rule    string `json:"rule"`
	Message string `json:"message"`
}

func (v Violation) String() string { return v.File + ": " + v.Message }

// Limits of a prototype.
const (
	MaxFiles     = 500
	MaxFileBytes = 5 << 20
	MaxBytes     = 25 << 20
)

// Manifest is manifest.json.
type Manifest struct {
	Format    int               `json:"format"`
	Revision  int               `json:"revision"`
	BuildID   string            `json:"buildId"`
	Title     string            `json:"title,omitempty"`
	Generator string            `json:"generator"`
	Files     map[string]string `json:"files"`
}

var allowedExt = map[string]bool{
	".html": true, ".css": true, ".js": true, ".json": true, ".svg": true, ".png": true, ".jpg": true, ".jpeg": true,
	".gif": true, ".webp": true, ".ico": true, ".woff": true, ".woff2": true, ".txt": true, ".md": true,
}

var (
	reModule     = regexp.MustCompile(`(?i)<script[^>]*\btype\s*=\s*["']?module`)
	reImportStmt = regexp.MustCompile(`(?m)^\s*import\s+[\w{*"']|^\s*export\s+(default|const|function|class|\{)|\bimport\s*\(`)
	reNetworkAPI = regexp.MustCompile(`\b(fetch|XMLHttpRequest|WebSocket|EventSource|importScripts|SharedWorker)\b|navigator\s*\.\s*sendBeacon|\bnew\s+Worker\b|serviceWorker`)
	reDynamic    = regexp.MustCompile(`\beval\s*\(|\bnew\s+Function\b|document\s*\.\s*write(ln)?\s*\(`)
	reAttrURL    = regexp.MustCompile(`(?is)<(script|link|img|source|video|audio|iframe|embed|object|track|image|use)\b[^>]*?\b(src|href|data|srcset|poster)\s*=\s*("([^"]*)"|'([^']*)'|([^\s>]+))`)
	reCSSURL     = regexp.MustCompile(`(?i)url\(\s*["']?([^"')]+)`)
	reCSSImport  = regexp.MustCompile(`(?i)@import\s+(url\(\s*)?["']?([^"')\s;]+)`)
	reFormAction = regexp.MustCompile(`(?is)<form\b[^>]*?\baction\s*=\s*("([^"]*)"|'([^']*)'|([^\s>]+))`)
	reMeta       = regexp.MustCompile(`(?is)<meta[^>]*http-equiv\s*=\s*["']?refresh`)
	reBase       = regexp.MustCompile(`(?is)<base\b`)
)

// external says whether an address reaches outside the folder: a scheme, a protocol-relative address or an absolute path.
// Fragments (#...) and data: addresses stay inside the document.
func external(u string) bool {
	u = strings.TrimSpace(u)
	if u == "" || strings.HasPrefix(u, "#") {
		return false
	}
	l := strings.ToLower(u)
	if strings.HasPrefix(l, "data:") {
		return false
	}
	return strings.HasPrefix(u, "/") || strings.Contains(l, ":") || strings.HasPrefix(u, "\\")
}

func firstNonEmpty(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}

// Check returns every rule the files break (nil when they keep them all), sorted by file and rule.
func Check(files map[string][]byte) []Violation {
	var out []Violation
	add := func(file, rule, format string, a ...any) {
		out = append(out, Violation{File: file, Rule: rule, Message: fmt.Sprintf(format, a...)})
	}

	if len(files) > MaxFiles {
		add("", "size", "too many files (%d, at most %d)", len(files), MaxFiles)
	}
	var total int
	for _, b := range files {
		total += len(b)
	}
	if total > MaxBytes {
		add("", "size", "the prototype is %d MB; the limit is %d MB", total>>20, MaxBytes>>20)
	}
	if _, ok := files["index.html"]; !ok {
		add("index.html", "entry", "there is no index.html at the top")
	}

	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		b := files[name]
		if strings.HasPrefix(name, "/") || strings.Contains(name, "..") || strings.Contains(name, `\`) || strings.Contains(name, "\x00") {
			add(name, "path", "the file name is not a plain relative path")
			continue
		}
		ext := strings.ToLower(path.Ext(name))
		if !allowedExt[ext] {
			add(name, "type", "files of type %q are not allowed", ext)
			continue
		}
		if len(b) > MaxFileBytes {
			add(name, "size", "the file is %d MB; the limit is %d MB", len(b)>>20, MaxFileBytes>>20)
		}
		s := string(b)
		switch ext {
		case ".html":
			if reModule.MatchString(s) {
				add(name, "classic-scripts", `a script has type="module"; use classic scripts so the prototype opens from a folder`)
			}
			for _, m := range reAttrURL.FindAllStringSubmatch(s, -1) {
				if u := firstNonEmpty(m[4], m[5], m[6]); external(u) {
					add(name, "relative-urls", "<%s %s=%q> reaches outside the folder; use a relative address", strings.ToLower(m[1]), strings.ToLower(m[2]), u)
				}
			}
			for _, m := range reFormAction.FindAllStringSubmatch(s, -1) {
				if a := firstNonEmpty(m[2], m[3], m[4]); a != "" && a != "#" {
					add(name, "forms", "a form submits to %q; forms must not submit anywhere", a)
				}
			}
			if reMeta.MatchString(s) {
				add(name, "relative-urls", "a meta refresh can leave the prototype")
			}
			if reBase.MatchString(s) {
				add(name, "relative-urls", "<base> changes what every relative address means")
			}
			checkCode(name, s, add)
			checkCSSInHTML(name, s, add)
		case ".css":
			for _, m := range reCSSImport.FindAllStringSubmatch(s, -1) {
				if external(m[2]) {
					add(name, "no-network", "@import %q loads from outside the folder", m[2])
				}
			}
			for _, m := range reCSSURL.FindAllStringSubmatch(s, -1) {
				if external(m[1]) {
					add(name, "no-network", "url(%q) loads from outside the folder", m[1])
				}
			}
		case ".js":
			checkCode(name, s, add)
		case ".svg":
			if strings.Contains(strings.ToLower(s), "<script") {
				add(name, "no-script-in-svg", "an SVG contains a script")
			}
			for _, m := range reCSSURL.FindAllStringSubmatch(s, -1) {
				if external(m[1]) {
					add(name, "no-network", "url(%q) loads from outside the folder", m[1])
				}
			}
		}
	}
	out = append(out, checkManifest(files)...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Rule < out[j].Rule
	})
	return out
}

func checkCSSInHTML(name, s string, add func(string, string, string, ...any)) {
	for _, m := range reCSSImport.FindAllStringSubmatch(s, -1) {
		if external(m[2]) {
			add(name, "no-network", "@import %q loads from outside the folder", m[2])
		}
	}
	for _, m := range reCSSURL.FindAllStringSubmatch(s, -1) {
		if external(m[1]) {
			add(name, "no-network", "url(%q) loads from outside the folder", m[1])
		}
	}
}

func checkCode(name, s string, add func(string, string, string, ...any)) {
	if m := reNetworkAPI.FindString(s); m != "" {
		add(name, "no-network", "uses %s; a prototype makes no network calls", strings.TrimSpace(m))
	}
	if reImportStmt.MatchString(s) {
		add(name, "classic-scripts", "uses import or export; use classic scripts (no modules)")
	}
	if m := reDynamic.FindString(s); m != "" {
		add(name, "no-eval", "uses %s; code must not be built from text", strings.TrimSpace(m))
	}
}

func checkManifest(files map[string][]byte) []Violation {
	var out []Violation
	add := func(rule, format string, a ...any) {
		out = append(out, Violation{File: "manifest.json", Rule: rule, Message: fmt.Sprintf(format, a...)})
	}
	raw, ok := files["manifest.json"]
	if !ok {
		add("manifest", "manifest.json is missing")
		return out
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		add("manifest", "manifest.json is not valid JSON")
		return out
	}
	if m.Revision < 1 {
		add("manifest", "the manifest does not say which spec revision this shows")
	}
	if m.BuildID == "" {
		add("manifest", "the manifest has no build id")
	}
	for name, want := range m.Files {
		b, ok := files[name]
		if !ok {
			add("manifest", "%s is listed but missing", name)
			continue
		}
		if got := sha(b); got != want {
			add("manifest", "%s does not match its recorded hash", name)
		}
	}
	for name := range files {
		if name == "manifest.json" {
			continue
		}
		if _, ok := m.Files[name]; !ok {
			add("manifest", "%s is not listed in the manifest", name)
		}
	}
	return out
}

// Sum is the hash a manifest records for a file: lower-case hex SHA-256.
func Sum(b []byte) string { return sha(b) }

func sha(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
