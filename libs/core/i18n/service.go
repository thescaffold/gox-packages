package i18n

// Service is the dependency-injected translation entry point, mirroring the TS
// LanguageService. Controllers inject a *Service and call Translate(path, data,
// preference) — the same surface the TS controller factories expose as
// `this.translate(path, data?, preference?)`.
//
// The argument order intentionally matches the TS controller method
// (path, data, preference), which differs from the lower-level Translate()
// function's (path, loader, preference, data) order.
type Service struct {
	loader Loader
}

// NewService constructs a translation Service backed by loader. In the wired
// application the loader is a MediaLoader over the shared MediaService cache.
func NewService(loader Loader) *Service {
	return &Service{loader: loader}
}

// Translate resolves a dotted i18n path to a rendered string, mirroring the TS
// controller `translate(path, data?, preference?)` which delegates to
// languageService.translate(path, mediaService, preference, data).
func (s *Service) Translate(path string, data, preference map[string]any) string {
	if s == nil || s.loader == nil {
		// No loader wired — degrade exactly like a miss: return the tail key.
		return Translate(path, noopLoader{}, preference, data)
	}
	return Translate(path, s.loader, preference, data)
}

// mediaStore is the minimal surface i18n needs from a media cache: a path→YAML
// lookup. *services/media.Service satisfies this structurally, so i18n does not
// import the media package (avoiding an import cycle).
type mediaStore interface {
	Get(path string) string
}

// MediaLoader adapts a media cache to the Loader interface. It mirrors how the
// TS translate() reads YAML via `mediaService.get(path)` — a store-only lookup
// that yields "" for an un-loaded path (the application pre-loads the
// translation paths at boot).
type MediaLoader struct {
	Store mediaStore
}

// Load returns the cached YAML for path, or "" when the path has not been
// pre-loaded. It never returns an error — matching media.get()'s '' fallback.
func (m MediaLoader) Load(path string) (string, error) {
	if m.Store == nil {
		return "", nil
	}
	return m.Store.Get(path), nil
}

// noopLoader is the fallback Loader used when a Service has no loader wired.
type noopLoader struct{}

func (noopLoader) Load(string) (string, error) { return "", nil }
