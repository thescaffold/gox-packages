package i18n

import (
	"strings"
	"sync"

	"github.com/cbroglie/mustache"
	"gopkg.in/yaml.v3"
)

// serviceCache caches parsed YAML maps keyed by service name.
var (
	serviceCache   = map[string]map[string]any{}
	translCache    = map[string]string{}
	cacheMu        sync.RWMutex
)

// Translate resolves a dotted i18n path to a rendered string.
//
// Path format: "[group.]service.key.subkey..."
//   - If the first segment contains "core", it is treated as the service name.
//   - Otherwise the first segment is the group, second is the service.
//
// YAML is loaded from loader at: "translations/{locale}/ntx[/{group}]/{service}.yaml"
// Falls back to "en" locale. Returns the tail key path if no value is found.
//
// Mustache rendering is applied with data + renderHelpers lambdas.
func Translate(path string, loader Loader, preference map[string]any, data map[string]any) string {
	path = strings.ToLower(path)
	parts := strings.SplitN(path, ".", 3)

	var groupName, serviceName, rest string
	switch {
	case len(parts) < 2:
		return path
	case strings.Contains(parts[0], "core"):
		// path = "coreXxx.key.subkey"
		serviceName = parts[0]
		if len(parts) > 1 {
			rest = strings.Join(parts[1:], ".")
		}
	default:
		// path = "group.service.key.subkey"
		groupName = parts[0]
		serviceName = parts[1]
		if len(parts) > 2 {
			rest = parts[2]
		}
	}

	// Cache hit (no dynamic data)
	if data == nil {
		cacheMu.RLock()
		if v, ok := translCache[path]; ok {
			cacheMu.RUnlock()
			return v
		}
		cacheMu.RUnlock()
	}

	// Check service YAML cache
	cacheMu.RLock()
	obj, hit := serviceCache[serviceName]
	cacheMu.RUnlock()

	if hit {
		value := getValueByDottedString(rest, obj)
		if value == "" || value == rest {
			return value
		}
		rendered := renderMustache(value, data)
		if data == nil {
			cacheMu.Lock()
			translCache[path] = rendered
			cacheMu.Unlock()
		}
		return rendered
	}

	// Load YAML
	locale := "en"
	if preference != nil {
		if lang, ok := preference["language"].(string); ok && lang != "" {
			locale = strings.ToLower(lang)
		}
	}
	group := ""
	if groupName != "" {
		group = "/" + groupName
	}
	yamlPath := "translations/" + locale + "/ntx" + group + "/" + serviceName + ".yaml"

	yamlStr, err := loader.Load(yamlPath)
	if err != nil && locale != "en" {
		fallback := "translations/en/ntx" + group + "/" + serviceName + ".yaml"
		yamlStr, _ = loader.Load(fallback)
	}

	if yamlStr == "" {
		return rest
	}

	parsed := map[string]any{}
	if err := yaml.Unmarshal([]byte(yamlStr), &parsed); err != nil || len(parsed) == 0 {
		return rest
	}

	cacheMu.Lock()
	serviceCache[serviceName] = parsed
	cacheMu.Unlock()

	value := getValueByDottedString(rest, parsed)
	if value == "" || value == rest {
		return value
	}

	rendered := renderMustache(value, data)
	if data == nil {
		cacheMu.Lock()
		translCache[path] = rendered
		cacheMu.Unlock()
	}
	return rendered
}

// getValueByDottedString traverses obj by a dotted key path.
// Returns the dotted string itself if any key is missing.
func getValueByDottedString(dottedString string, obj map[string]any) string {
	if dottedString == "" {
		return ""
	}
	keys := strings.Split(dottedString, ".")
	var current any = obj
	for _, k := range keys {
		m, ok := current.(map[string]any)
		if !ok {
			return dottedString
		}
		v, found := m[k]
		if !found {
			return dottedString
		}
		current = v
	}
	if s, ok := current.(string); ok {
		return s
	}
	return dottedString
}

// renderMustache renders a Mustache template string with data + render helpers.
func renderMustache(tmpl string, data map[string]any) string {
	ctx := map[string]any{}
	for k, v := range RenderHelpers {
		ctx[k] = v
	}
	for k, v := range data {
		ctx[k] = v
	}
	out, err := mustache.Render(tmpl, ctx)
	if err != nil {
		return tmpl
	}
	return out
}

// ResetCache clears all caches — intended for testing only.
func ResetCache() {
	cacheMu.Lock()
	serviceCache = map[string]map[string]any{}
	translCache = map[string]string{}
	cacheMu.Unlock()
}
