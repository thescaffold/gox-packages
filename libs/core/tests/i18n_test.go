package tests

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	test "github.com/awesome-goose/goose/testing"
	"github.com/thescaffold/gox-packages-core/i18n"
)

func TestI18n(t *testing.T) {
	test.NewSuiteRunner(t, &I18nSuite{}).Run()
}

type I18nSuite struct {
	test.Suite
}

// testDir returns the absolute path to the tests directory.
func testDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(file)
}

// newLoader returns a FileLoader rooted at the tests directory.
func newLoader() *i18n.FileLoader {
	return &i18n.FileLoader{BaseDir: testDir()}
}

// reset clears caches before each test to avoid cross-test pollution.
func reset() {
	i18n.ResetCache()
}

// ── Key resolution ─────────────────────────────────────────────────────────────

func (s *I18nSuite) TestTranslate_SimpleKey() {
	reset()
	result := i18n.Translate("users.users.created", newLoader(), nil, nil)
	s.T.Expect(result).ToEqual("User created successfully")
}

func (s *I18nSuite) TestTranslate_CachesResult() {
	reset()
	r1 := i18n.Translate("users.users.created", newLoader(), nil, nil)
	r2 := i18n.Translate("users.users.created", newLoader(), nil, nil)
	s.T.Expect(r1).ToEqual(r2)
}

func (s *I18nSuite) TestTranslate_Locale_FR() {
	reset()
	result := i18n.Translate("users.users.created", newLoader(), map[string]any{"language": "fr"}, nil)
	s.T.Expect(result).ToEqual("Utilisateur créé avec succès")
}

func (s *I18nSuite) TestTranslate_FallbackToEN_UnknownLocale() {
	reset()
	result := i18n.Translate("users.users.created", newLoader(), map[string]any{"language": "de"}, nil)
	s.T.Expect(result).ToEqual("User created successfully")
}

func (s *I18nSuite) TestTranslate_MissingKey_ReturnsTailPath() {
	reset()
	result := i18n.Translate("users.users.missing.deep", newLoader(), nil, nil)
	s.T.Expect(result).ToEqual("missing.deep")
}

func (s *I18nSuite) TestTranslate_MissingService_ReturnsKey() {
	reset()
	result := i18n.Translate("users.nonexistent.key", newLoader(), nil, nil)
	// loader returns error → result should be the rest key
	s.T.Expect(result).ToEqual("key")
}

// ── Mustache rendering ─────────────────────────────────────────────────────────

func (s *I18nSuite) TestTranslate_MustacheInterpolation() {
	reset()
	result := i18n.Translate("users.users.greeting", newLoader(), nil, map[string]any{"name": "Alice"})
	s.T.Expect(result).ToEqual("Hello, Alice!")
}

func (s *I18nSuite) TestTranslate_NoDataSkipsMustache() {
	reset()
	// Without data the template is returned with empty substitution (mustache renders {{name}} as "")
	result := i18n.Translate("users.users.greeting", newLoader(), nil, nil)
	s.T.Expect(result).ToEqual("Hello, !")
}

// ── renderHelpers ──────────────────────────────────────────────────────────────

func (s *I18nSuite) TestXSanitizer_RemovesSpecialChars() {
	reset()
	result := i18n.Translate("users.users.sanitized", newLoader(), nil, map[string]any{"value": "Hello, World! 123"})
	// xSanitizer strips non-alphanum, lowercases, removes leading digits
	s.T.Expect(result).ToEqual("helloworld123")
}

func (s *I18nSuite) TestXMajor_ConvertsMinorToMajor() {
	reset()
	result := i18n.Translate("users.users.amount", newLoader(), nil, map[string]any{"cents": "1050"})
	s.T.Expect(result).ToEqual("Amount: 10.50")
}

// ── getValueByDottedString (via Translate) ─────────────────────────────────────

func (s *I18nSuite) TestTranslate_DeepKey_NotFound() {
	reset()
	result := i18n.Translate("users.users.a.b.c", newLoader(), nil, nil)
	s.T.Expect(result).ToEqual("a.b.c")
}

// ── FileLoader ─────────────────────────────────────────────────────────────────

func (s *I18nSuite) TestFileLoader_MissingFile_ReturnsError() {
	loader := &i18n.FileLoader{BaseDir: testDir()}
	_, err := loader.Load("translations/en/ntx/nope/nope.yaml")
	s.T.Expect(err == nil).ToEqual(false)
}

func (s *I18nSuite) TestFileLoader_ExistingFile_ReturnsContent() {
	loader := &i18n.FileLoader{BaseDir: testDir()}
	content, err := loader.Load("translations/en/ntx/users/users.yaml")
	s.T.Expect(err).ToBeNil()
	s.T.Expect(content == "").ToEqual(false)
}

// ── Verify translation fixture files exist ────────────────────────────────────

func (s *I18nSuite) TestFixture_ENFile_Exists() {
	path := filepath.Join(testDir(), "translations", "en", "ntx", "users", "users.yaml")
	_, err := os.Stat(path)
	s.T.Expect(err).ToBeNil()
}

func (s *I18nSuite) TestFixture_FRFile_Exists() {
	path := filepath.Join(testDir(), "translations", "fr", "ntx", "users", "users.yaml")
	_, err := os.Stat(path)
	s.T.Expect(err).ToBeNil()
}
