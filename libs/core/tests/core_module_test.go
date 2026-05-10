package tests

import (
	"testing"

	test "github.com/awesome-goose/goose/testing"
	"github.com/thescaffold/gox-packages-core/module"
)

func TestCoreModule(t *testing.T) {
	test.NewSuiteRunner(t, &CoreModuleSuite{}).Run()
}

type CoreModuleSuite struct {
	test.Suite
}

func (s *CoreModuleSuite) TestNew_ReturnsModule() {
	m := module.New(module.CoreConfig{HMACKey: "key"})
	s.T.Expect(m == nil).ToEqual(false)
}

func (s *CoreModuleSuite) TestImports_ReturnsNil() {
	m := module.New(module.CoreConfig{})
	s.T.Expect(m.Imports() == nil).ToEqual(true)
}

func (s *CoreModuleSuite) TestDeclarations_NonEmpty() {
	m := module.New(module.CoreConfig{HMACKey: "key"})
	decls := m.Declarations()
	s.T.Expect(len(decls) > 0).ToEqual(true)
}

func (s *CoreModuleSuite) TestExports_MatchDeclarations() {
	m := module.New(module.CoreConfig{HMACKey: "key"})
	s.T.Expect(len(m.Exports())).ToEqual(len(m.Declarations()))
}

func (s *CoreModuleSuite) TestDeclarations_ContainsExpectedCount() {
	m := module.New(module.CoreConfig{HMACKey: "key"})
	// 16 declarations total: bus, tracker, httpClient, ws, ctx middleware,
	// error filter, image, cache, sync, batch, marker, otp, platform, text,
	// throttler, uaparser.
	s.T.Expect(len(m.Declarations())).ToEqual(16)
}
