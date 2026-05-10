package tests

import (
	"strings"
	"testing"
	"time"

	test "github.com/awesome-goose/goose/testing"
	"github.com/thescaffold/gox-packages-core/utils"
)

func TestUtils(t *testing.T) {
	test.NewSuiteRunner(t, &UtilsSuite{}).Run()
}

type UtilsSuite struct {
	test.Suite
}

// --- random ---

func (s *UtilsSuite) TestRandomDigits_Length() {
	r := utils.RandomDigits(6)
	s.T.Expect(len(r)).ToEqual(6)
}

func (s *UtilsSuite) TestRandomDigits_OnlyNumbers() {
	r := utils.RandomDigits(20)
	for _, c := range r {
		s.T.Expect(c >= '0' && c <= '9').ToEqual(true)
	}
}

func (s *UtilsSuite) TestRandom_Length() {
	r := utils.Random(10)
	s.T.Expect(len(r)).ToEqual(10)
}

func (s *UtilsSuite) TestRandomUsername_Format() {
	u := utils.RandomUsername()
	parts := strings.Split(u, "-")
	s.T.Expect(len(parts)).ToEqual(4)
	s.T.Expect(len(parts[0])).ToEqual(1)
	s.T.Expect(len(parts[1])).ToEqual(3)
	s.T.Expect(len(parts[2])).ToEqual(3)
	s.T.Expect(len(parts[3])).ToEqual(3)
	s.T.Expect(u).ToEqual(strings.ToUpper(u))
}

func (s *UtilsSuite) TestRandomReferralCode_Format() {
	c := utils.RandomReferralCode("O")
	parts := strings.Split(c, "-")
	s.T.Expect(len(parts)).ToEqual(3)
	s.T.Expect(parts[0]).ToEqual("O")
	s.T.Expect(len(parts[1])).ToEqual(3)
	s.T.Expect(len(parts[2])).ToEqual(3)
}

func (s *UtilsSuite) TestRandomReferralCode_DefaultInitial() {
	c := utils.RandomReferralCode("")
	s.T.Expect(strings.HasPrefix(c, "O-")).ToEqual(true)
}

// --- uuid ---

func (s *UtilsSuite) TestUUID_Format() {
	id := utils.UUID()
	// basic UUID v4: 8-4-4-4-12
	parts := strings.Split(id, "-")
	s.T.Expect(len(parts)).ToEqual(5)
}

func (s *UtilsSuite) TestReference_MaxLen() {
	ref := utils.Reference("NTX", 36)
	s.T.Expect(len(ref) <= 36).ToEqual(true)
}

func (s *UtilsSuite) TestReference_Uppercase() {
	ref := utils.Reference("svc", 50)
	s.T.Expect(ref).ToEqual(strings.ToUpper(ref))
}

func (s *UtilsSuite) TestReference_StartsWithService() {
	ref := utils.Reference("MYAPP", 50)
	s.T.Expect(strings.HasPrefix(ref, "MYAPP-")).ToEqual(true)
}

// --- string ---

func (s *UtilsSuite) TestUCFirst() {
	s.T.Expect(utils.UCFirst("hello world")).ToEqual("Hello world")
}

func (s *UtilsSuite) TestUCFirst_Empty() {
	s.T.Expect(utils.UCFirst("")).ToEqual("")
}

func (s *UtilsSuite) TestTitleCase() {
	s.T.Expect(utils.TitleCase("hello world")).ToEqual("Hello World")
}

func (s *UtilsSuite) TestPrettify() {
	s.T.Expect(utils.Prettify("hello_world")).ToEqual("hello world")
}

func (s *UtilsSuite) TestTrimString_RemovesSlashes() {
	s.T.Expect(utils.TrimString("/foo/bar/", "")).ToEqual("foo/bar")
}

func (s *UtilsSuite) TestMaskEmail() {
	s.T.Expect(utils.MaskEmail("john.doe@example.com")).ToEqual("joh***oe@example.com")
}

func (s *UtilsSuite) TestMaskEmail_Short() {
	// ≤5 chars in username — no masking
	s.T.Expect(utils.MaskEmail("ab@x.com")).ToEqual("ab@x.com")
}

// --- time ---

func (s *UtilsSuite) TestNow_IsUTC() {
	t := utils.Now()
	s.T.Expect(t.Location()).ToEqual(time.UTC)
}

func (s *UtilsSuite) TestFormat_FullDate() {
	t := time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC)
	s.T.Expect(utils.Format(t, utils.FormatFullDate)).ToEqual("2024-03-15")
}

func (s *UtilsSuite) TestFormat_Date() {
	t := time.Date(2024, 3, 5, 0, 0, 0, 0, time.UTC)
	s.T.Expect(utils.Format(t, utils.FormatDate)).ToEqual("05-03")
}

// --- money ---

func (s *UtilsSuite) TestToMajor_Basic() {
	s.T.Expect(utils.ToMajor(1050, 2)).ToEqual("10.50")
}

func (s *UtilsSuite) TestToMajor_Zero() {
	s.T.Expect(utils.ToMajor(0, 2)).ToEqual("0.00")
}

func (s *UtilsSuite) TestToMinor_Basic() {
	v, err := utils.ToMinor("10.50", 2)
	s.T.Expect(err).ToBeNil()
	s.T.Expect(v).ToEqual(int64(1050))
}

func (s *UtilsSuite) TestToMinor_NoDecimal() {
	v, err := utils.ToMinor("10", 2)
	s.T.Expect(err).ToBeNil()
	s.T.Expect(v).ToEqual(int64(1000))
}

func (s *UtilsSuite) TestToInt_Basic() {
	v, err := utils.ToInt("42.99")
	s.T.Expect(err).ToBeNil()
	s.T.Expect(v).ToEqual(int64(42))
}

// --- validate ---

func (s *UtilsSuite) TestIsEmail_Valid() {
	s.T.Expect(utils.IsEmail("user@example.com")).ToEqual(true)
}

func (s *UtilsSuite) TestIsEmail_Invalid() {
	s.T.Expect(utils.IsEmail("notanemail")).ToEqual(false)
}

func (s *UtilsSuite) TestIsPhone_Valid() {
	s.T.Expect(utils.IsPhone("08012345678")).ToEqual(true)
}

func (s *UtilsSuite) TestIsPhone_WithCountryCode() {
	s.T.Expect(utils.IsPhone("+2348012345678")).ToEqual(true)
}

func (s *UtilsSuite) TestIsPhone_Invalid() {
	s.T.Expect(utils.IsPhone("12345")).ToEqual(false)
}

func (s *UtilsSuite) TestIsNumeric_Integer() {
	s.T.Expect(utils.IsNumeric("42")).ToEqual(true)
}

func (s *UtilsSuite) TestIsNumeric_Decimal() {
	s.T.Expect(utils.IsNumeric("3.14")).ToEqual(true)
}

func (s *UtilsSuite) TestIsNumeric_Negative() {
	s.T.Expect(utils.IsNumeric("-7")).ToEqual(true)
}

func (s *UtilsSuite) TestIsNumeric_String() {
	s.T.Expect(utils.IsNumeric("abc")).ToEqual(false)
}

func (s *UtilsSuite) TestIsNullOrUndefined_Nil() {
	s.T.Expect(utils.IsNullOrUndefined(nil)).ToEqual(true)
}

func (s *UtilsSuite) TestIsNullOrUndefined_NotNil() {
	s.T.Expect(utils.IsNullOrUndefined("hello")).ToEqual(false)
}
