package tests

import (
	"testing"

	test "github.com/awesome-goose/goose/testing"
	"github.com/thescaffold/gox-packages/libs/core/services/uaparser"
)

func TestUAParser(t *testing.T) {
	test.NewSuiteRunner(t, &UAParserSuite{}).Run()
}

type UAParserSuite struct {
	test.Suite
}

// All expected strings below are the literal `Object.values(getXxx()).join('/')`
// output of ua-parser-js v2.0.9 (the library the TS service wraps), captured by
// running the library directly. ua-parser-js initialises every group with all
// keys present (undefined → "" after join), so an empty/unknown UA yields the
// empty-slot skeleton rather than an empty string.

// TestParse_Empty: setUA("") still emits the skeleton — browser "///", os "/",
// engine "/", device "//", cpu "".
func (s *UAParserSuite) TestParse_Empty() {
	r := uaparser.New().Parse("")
	s.T.Expect(r.Browser).ToEqual("///")
	s.T.Expect(r.OS).ToEqual("/")
	s.T.Expect(r.Engine).ToEqual("/")
	s.T.Expect(r.Device).ToEqual("//")
	s.T.Expect(r.CPU).ToEqual("")
}

func (s *UAParserSuite) TestParse_Chrome_macOS() {
	const ua = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.6099.225 Safari/537.36"
	r := uaparser.New().Parse(ua)
	// browser = name/version/major/type (type empty → trailing slash).
	s.T.Expect(r.Browser).ToEqual("Chrome/120.0.6099.225/120/")
	// ua-parser-js v2 reports "macOS" and normalises underscores to dots.
	s.T.Expect(r.OS).ToEqual("macOS/10.15.7")
	// Chrome on AppleWebKit → Blink, versioned by the Chrome version.
	s.T.Expect(r.Engine).ToEqual("Blink/120.0.6099.225")
	// macOS desktop → {type:undefined, model:"Macintosh", vendor:"Apple"}.
	s.T.Expect(r.Device).ToEqual("/Macintosh/Apple")
	// Intel Mac is NOT inferred as amd64 by ua-parser-js.
	s.T.Expect(r.CPU).ToEqual("")
}

func (s *UAParserSuite) TestParse_Safari_iOS() {
	const ua = "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1"
	r := uaparser.New().Parse(ua)
	s.T.Expect(r.Browser).ToEqual("Mobile Safari/17.5/17/")
	s.T.Expect(r.OS).ToEqual("iOS/17.5.1")
	// Pure WebKit on iOS — no Chrome token, so engine stays WebKit.
	s.T.Expect(r.Engine).ToEqual("WebKit/605.1.15")
	// device = type/model/vendor.
	s.T.Expect(r.Device).ToEqual("mobile/iPhone/Apple")
}

func (s *UAParserSuite) TestParse_Firefox_Ubuntu() {
	const ua = "Mozilla/5.0 (X11; Ubuntu; Linux x86_64; rv:128.0) Gecko/20100101 Firefox/128.0"
	r := uaparser.New().Parse(ua)
	s.T.Expect(r.Browser).ToEqual("Firefox/128.0/128/")
	s.T.Expect(r.OS).ToEqual("Ubuntu/")
	s.T.Expect(r.Engine).ToEqual("Gecko/128.0")
	s.T.Expect(r.CPU).ToEqual("amd64")
}

func (s *UAParserSuite) TestParse_Edge_Windows() {
	const ua = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36 Edg/120.0.2210.91"
	r := uaparser.New().Parse(ua)
	s.T.Expect(r.Browser).ToEqual("Edge/120.0.2210.91/120/")
	// NT 10.0 maps to the marketing name "10".
	s.T.Expect(r.OS).ToEqual("Windows/10")
	s.T.Expect(r.Engine).ToEqual("Blink/120.0.0.0")
	s.T.Expect(r.CPU).ToEqual("amd64")
}

func (s *UAParserSuite) TestParse_Windows7() {
	const ua = "Mozilla/5.0 (Windows NT 6.1; WOW64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/100.0.0.0 Safari/537.36"
	r := uaparser.New().Parse(ua)
	// NT 6.1 maps to "7".
	s.T.Expect(r.OS).ToEqual("Windows/7")
}

func (s *UAParserSuite) TestParse_AndroidChrome() {
	const ua = "Mozilla/5.0 (Linux; Android 14; Pixel 8 Build/UQ1A.231205.015) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.6099.43 Mobile Safari/537.36"
	r := uaparser.New().Parse(ua)
	// Android Chrome → "Mobile Chrome".
	s.T.Expect(r.Browser).ToEqual("Mobile Chrome/120.0.6099.43/120/")
	s.T.Expect(r.OS).ToEqual("Android/14")
	// device = type/model/vendor; Pixel → Google vendor.
	s.T.Expect(r.Device).ToEqual("mobile/Pixel 8/Google")
}

func (s *UAParserSuite) TestParse_SamsungInternet() {
	const ua = "Mozilla/5.0 (Linux; Android 13; SM-G990B) AppleWebKit/537.36 (KHTML, like Gecko) SamsungBrowser/22.0 Chrome/115.0.0.0 Mobile Safari/537.36"
	r := uaparser.New().Parse(ua)
	// Samsung Internet rule wins over Chrome (no "Mobile " prefix for it).
	s.T.Expect(r.Browser).ToEqual("Samsung Internet/22.0/22/")
	s.T.Expect(r.Device).ToEqual("mobile/SM-G990B/Samsung")
}

// TestParse_OperaWinsOverChrome ensures the rule-order discipline holds —
// Opera UA strings include both OPR/ and Chrome/ tokens; our Opera rule fires
// first or the Chrome rule masks it.
func (s *UAParserSuite) TestParse_OperaWinsOverChrome() {
	const ua = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36 OPR/105.0.4970.34"
	r := uaparser.New().Parse(ua)
	s.T.Expect(r.Browser).ToEqual("Opera/105.0.4970.34/105/")
	// Blink version is the Chrome version, not the OPR version.
	s.T.Expect(r.Engine).ToEqual("Blink/120.0.0.0")
}

func (s *UAParserSuite) TestParse_IE11() {
	const ua = "Mozilla/5.0 (Windows NT 10.0; WOW64; Trident/7.0; rv:11.0) like Gecko"
	r := uaparser.New().Parse(ua)
	s.T.Expect(r.Browser).ToEqual("IE/11.0/11/")
	s.T.Expect(r.Engine).ToEqual("Trident/7.0")
}

func (s *UAParserSuite) TestParse_AppleSilicon_arm64() {
	// arm64 / aarch64 tokens drive arm64 classification.
	r := uaparser.New().Parse("Mozilla/5.0 (Macintosh; arm64) AppleWebKit/605.1.15")
	s.T.Expect(r.CPU).ToEqual("arm64")
}
