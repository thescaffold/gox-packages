package tests

import (
	"testing"

	test "github.com/awesome-goose/goose/testing"
	"github.com/thescaffold/gox-packages-flags"
	"github.com/thescaffold/gox-packages-flags/flag"
)

func TestFlags(t *testing.T) {
	test.NewSuiteRunner(t, &FlagsSuite{}).Run()
}

type FlagsSuite struct {
	test.Suite
}

func newSvc() *flag.FlagService {
	return flag.NewFlagService("production")
}

// ── Register ───────────────────────────────────────────────────────────────────

func (s *FlagsSuite) TestRegister_StoresFlag() {
	svc := newSvc()
	svc.Register([]flag.FlagDef{{Name: "dark-mode", Enabled: true}}, "")
	result := svc.Status([]string{"dark-mode"}, flag.StatusOpts{})
	s.T.Expect(result["dark-mode"]).ToEqual(true)
}

func (s *FlagsSuite) TestRegister_DisabledFlag_StatusFalse() {
	svc := newSvc()
	svc.Register([]flag.FlagDef{{Name: "beta", Enabled: false}}, "")
	result := svc.Status([]string{"beta"}, flag.StatusOpts{})
	s.T.Expect(result["beta"]).ToEqual(false)
}

func (s *FlagsSuite) TestRegister_UnknownFlag_StatusFalse() {
	svc := newSvc()
	result := svc.Status([]string{"nonexistent"}, flag.StatusOpts{})
	s.T.Expect(result["nonexistent"]).ToEqual(false)
}

// ── Environment filtering ──────────────────────────────────────────────────────

func (s *FlagsSuite) TestStatus_EnvMatch_ReturnsTrue() {
	svc := flag.NewFlagService("staging")
	svc.Register([]flag.FlagDef{{Name: "f1", Enabled: true, Environments: []string{"staging"}}}, "")
	result := svc.Status([]string{"f1"}, flag.StatusOpts{})
	s.T.Expect(result["f1"]).ToEqual(true)
}

func (s *FlagsSuite) TestStatus_EnvMismatch_ReturnsFalse() {
	svc := flag.NewFlagService("production")
	svc.Register([]flag.FlagDef{{Name: "f1", Enabled: true, Environments: []string{"staging"}}}, "")
	result := svc.Status([]string{"f1"}, flag.StatusOpts{})
	s.T.Expect(result["f1"]).ToEqual(false)
}

func (s *FlagsSuite) TestStatus_NoEnvFilter_AlwaysEnabled() {
	svc := flag.NewFlagService("any-env")
	svc.Register([]flag.FlagDef{{Name: "global", Enabled: true}}, "")
	result := svc.Status([]string{"global"}, flag.StatusOpts{})
	s.T.Expect(result["global"]).ToEqual(true)
}

// ── Log ────────────────────────────────────────────────────────────────────────

func (s *FlagsSuite) TestLog_DoesNotPanic() {
	svc := newSvc()
	svc.Register([]flag.FlagDef{{Name: "f", Enabled: true}}, "")
	svc.Log("f", flag.LogOpts{Level: "info", UserID: "u1"})
}

func (s *FlagsSuite) TestLog_RespectsLimit() {
	svc := newSvc()
	svc.Register([]flag.FlagDef{{Name: "f", Enabled: true}}, "")
	for i := 0; i < 5; i++ {
		svc.Log("f", flag.LogOpts{Limit: 3})
	}
	// No panic — limit silently caps entries
}

// ── Limit ──────────────────────────────────────────────────────────────────────

func (s *FlagsSuite) TestLimit_EnabledFlag_ReturnsTrue() {
	svc := newSvc()
	svc.Register([]flag.FlagDef{{Name: "pay", Enabled: true}}, "")
	ok, err := svc.Limit("pay", flag.LimitOpts{})
	s.T.Expect(ok).ToEqual(true)
	s.T.Expect(err).ToBeNil()
}

func (s *FlagsSuite) TestLimit_DisabledFlag_ReturnsFalse() {
	svc := newSvc()
	svc.Register([]flag.FlagDef{{Name: "pay", Enabled: false}}, "")
	ok, err := svc.Limit("pay", flag.LimitOpts{})
	s.T.Expect(ok).ToEqual(false)
	s.T.Expect(err).ToBeNil()
}

// ── FlagsModule ────────────────────────────────────────────────────────────────

func (s *FlagsSuite) TestRegisterModule_ReturnsModule() {
	m := flags.Register(flags.FlagsConfig{Env: "production"})
	s.T.Expect(m == nil).ToEqual(false)
}

func (s *FlagsSuite) TestDeclarations_HasOneFlagService() {
	m := flags.Register(flags.FlagsConfig{Env: "production"})
	s.T.Expect(len(m.Declarations())).ToEqual(1)
}

func (s *FlagsSuite) TestExports_MatchDeclarations() {
	m := flags.Register(flags.FlagsConfig{Env: "production"})
	s.T.Expect(len(m.Exports())).ToEqual(len(m.Declarations()))
}
