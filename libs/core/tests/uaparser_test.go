package tests

import (
	"strings"
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

// TestParse_Empty returns zero-value Result with empty strings, matching the TS
// stringify which yields "" when ua-parser-js can't classify the input.
func (s *UAParserSuite) TestParse_Empty() {
	r := uaparser.New().Parse("")
	s.T.Expect(r.Browser).ToEqual("")
	s.T.Expect(r.OS).ToEqual("")
}

// TestParse_Chrome_macOS verifies a recent Chrome-on-macOS UA decomposes into
// the same five fields ua-parser-js would emit, joined with `/` to match TS
// stringify().
func (s *UAParserSuite) TestParse_Chrome_macOS() {
	const ua = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.6099.225 Safari/537.36"
	r := uaparser.New().Parse(ua)
	// Browser is "name/version/major" — Object.values({name, version, major}).
	s.T.Expect(r.Browser).ToEqual("Chrome/120.0.6099.225/120")
	// OS is "name/version" with dots (TS normalizes underscores).
	s.T.Expect(r.OS).ToEqual("Mac OS/10.15.7")
	// Chrome on AppleWebKit reports Blink engine (modern ua-parser-js behaviour).
	s.T.Expect(strings.HasPrefix(r.Engine, "Blink/")).ToEqual(true)
	// Desktop Mac → empty device.
	s.T.Expect(r.Device).ToEqual("")
	// Intel Mac → amd64.
	s.T.Expect(r.CPU).ToEqual("amd64")
}

func (s *UAParserSuite) TestParse_Safari_iOS() {
	const ua = "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1"
	r := uaparser.New().Parse(ua)
	s.T.Expect(strings.HasPrefix(r.Browser, "Mobile Safari/")).ToEqual(true)
	s.T.Expect(r.OS).ToEqual("iOS/17.5.1")
	// Pure WebKit on iOS — no Chrome token, so engine stays WebKit.
	s.T.Expect(strings.HasPrefix(r.Engine, "WebKit/")).ToEqual(true)
	s.T.Expect(r.Device).ToEqual("iPhone/mobile/Apple")
}

func (s *UAParserSuite) TestParse_Firefox_Linux() {
	const ua = "Mozilla/5.0 (X11; Ubuntu; Linux x86_64; rv:128.0) Gecko/20100101 Firefox/128.0"
	r := uaparser.New().Parse(ua)
	s.T.Expect(r.Browser).ToEqual("Firefox/128.0/128")
	s.T.Expect(strings.HasPrefix(r.OS, "Ubuntu")).ToEqual(true)
	s.T.Expect(strings.HasPrefix(r.Engine, "Gecko/")).ToEqual(true)
	s.T.Expect(r.CPU).ToEqual("amd64")
}

func (s *UAParserSuite) TestParse_Edge_Windows() {
	const ua = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36 Edg/120.0.2210.91"
	r := uaparser.New().Parse(ua)
	s.T.Expect(r.Browser).ToEqual("Edge/120.0.2210.91/120")
	s.T.Expect(strings.HasPrefix(r.OS, "Windows/")).ToEqual(true)
	s.T.Expect(strings.HasPrefix(r.Engine, "Blink/")).ToEqual(true)
	s.T.Expect(r.CPU).ToEqual("amd64")
}

func (s *UAParserSuite) TestParse_AndroidChrome() {
	const ua = "Mozilla/5.0 (Linux; Android 14; Pixel 8 Build/UQ1A.231205.015) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.6099.43 Mobile Safari/537.36"
	r := uaparser.New().Parse(ua)
	s.T.Expect(strings.HasPrefix(r.Browser, "Chrome/120")).ToEqual(true)
	s.T.Expect(r.OS).ToEqual("Android/14")
	// Pixel triggers Google vendor + mobile type.
	s.T.Expect(r.Device).ToEqual("Pixel 8/mobile/Google")
}

func (s *UAParserSuite) TestParse_SamsungInternet() {
	const ua = "Mozilla/5.0 (Linux; Android 13; SM-G990B) AppleWebKit/537.36 (KHTML, like Gecko) SamsungBrowser/22.0 Chrome/115.0.0.0 Mobile Safari/537.36"
	r := uaparser.New().Parse(ua)
	// Samsung Internet rule wins over Chrome because the rule order is
	// intentional — UA strings contain both tokens.
	s.T.Expect(strings.HasPrefix(r.Browser, "Samsung Internet/22.0")).ToEqual(true)
	// SM-G990B → Samsung Galaxy S21 FE, classified as Samsung.
	s.T.Expect(strings.HasPrefix(r.Device, "SM-G990B/mobile/Samsung")).ToEqual(true)
}

// TestParse_OperaWinsOverChrome ensures the rule-order discipline holds —
// Opera UA strings include both OPR/ and Chrome/ tokens; our Opera rule must
// fire first or the Chrome rule masks it.
func (s *UAParserSuite) TestParse_OperaWinsOverChrome() {
	const ua = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36 OPR/105.0.4970.34"
	r := uaparser.New().Parse(ua)
	s.T.Expect(strings.HasPrefix(r.Browser, "Opera/")).ToEqual(true)
}

func (s *UAParserSuite) TestParse_IE11() {
	const ua = "Mozilla/5.0 (Windows NT 10.0; WOW64; Trident/7.0; rv:11.0) like Gecko"
	r := uaparser.New().Parse(ua)
	s.T.Expect(r.Browser).ToEqual("IE/11.0/11")
	s.T.Expect(strings.HasPrefix(r.Engine, "Trident/")).ToEqual(true)
}

func (s *UAParserSuite) TestParse_AppleSilicon_arm64() {
	// Modern Safari on M-series macs still reports x86_64 for compat. To detect
	// arm64 the UA would need to explicitly carry arm64 / aarch64 tokens.
	r := uaparser.New().Parse("Mozilla/5.0 (Macintosh; arm64) AppleWebKit/605.1.15")
	s.T.Expect(r.CPU).ToEqual("arm64")
}
