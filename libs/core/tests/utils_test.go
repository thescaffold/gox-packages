package tests

import (
	"strings"
	"testing"
	"time"

	test "github.com/awesome-goose/goose/testing"
	"github.com/thescaffold/gox-packages/libs/core/utils"
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

func (s *UtilsSuite) TestTitleCase_LowercasesRestAndKeepsSpacing() {
	// TS titleCase lower-cases non-word-start chars and preserves spacing.
	s.T.Expect(utils.TitleCase("HELLO  WORLD")).ToEqual("Hello  World")
}

func (s *UtilsSuite) TestPrettify() {
	s.T.Expect(utils.Prettify("hello_world")).ToEqual("hello world")
}

func (s *UtilsSuite) TestTrimString_RemovesSlashes() {
	s.T.Expect(utils.TrimString("/foo/bar/", "")).ToEqual("foo/bar")
}

func (s *UtilsSuite) TestMaskEmail() {
	// TS maskEmail: slice(0,3) + '*'.repeat(len-2) + slice(-2). "john.doe" → 6 stars.
	s.T.Expect(utils.MaskEmail("john.doe@example.com")).ToEqual("joh******oe@example.com")
}

func (s *UtilsSuite) TestMaskEmail_Short() {
	// Short usernames: visible start/end overlap, zero stars — matches TS.
	s.T.Expect(utils.MaskEmail("ab@x.com")).ToEqual("abab@x.com")
}

func (s *UtilsSuite) TestMask_FixedSevenStars() {
	s.T.Expect(utils.Mask("anything")).ToEqual("*******")
	s.T.Expect(utils.Mask("")).ToEqual("")
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
	v, err := utils.ToMajor(1050)
	s.T.Expect(err).ToBeNil()
	s.T.Expect(v).ToEqual("10.50")
}

func (s *UtilsSuite) TestToMajor_Zero() {
	v, err := utils.ToMajor(0)
	s.T.Expect(err).ToBeNil()
	s.T.Expect(v).ToEqual("0.00")
}

func (s *UtilsSuite) TestToMajor_PadsShortValue() {
	// TS toMajor pads to >=3 digits before slicing: 5 → "0.05".
	v, err := utils.ToMajor(5)
	s.T.Expect(err).ToBeNil()
	s.T.Expect(v).ToEqual("0.05")
}

func (s *UtilsSuite) TestToMinor_Basic() {
	v, err := utils.ToMinor("10.50")
	s.T.Expect(err).ToBeNil()
	s.T.Expect(v).ToEqual(int64(1050))
}

func (s *UtilsSuite) TestToMinor_NoDecimal() {
	v, err := utils.ToMinor("10")
	s.T.Expect(err).ToBeNil()
	s.T.Expect(v).ToEqual(int64(1000))
}

func (s *UtilsSuite) TestToMinor_SingleFractionDigit() {
	// TS pads the fraction with "00" then truncates to 2: "10.5" → 1050.
	v, err := utils.ToMinor("10.5")
	s.T.Expect(err).ToBeNil()
	s.T.Expect(v).ToEqual(int64(1050))
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

func (s *UtilsSuite) TestIsPhone_LocalPrefix234() {
	// 234-prefixed, length 13 — mirrors TS isPhoneNumber.
	s.T.Expect(utils.IsPhone("2348012345678")).ToEqual(true)
}

func (s *UtilsSuite) TestIsPhone_DigitPatternNotConstrained() {
	// TS isPhoneNumber only checks numeric + length{11,13,14} + prefix; it does
	// NOT enforce the [789][01] mobile shape. "01234567890" starts with 0, is
	// 11 digits and numeric, so it is valid in TS and must be valid here too.
	s.T.Expect(utils.IsPhone("01234567890")).ToEqual(true)
}

func (s *UtilsSuite) TestIsPhone_WrongLength() {
	// 10 digits — not in {11,13,14}.
	s.T.Expect(utils.IsPhone("0801234567")).ToEqual(false)
}

func (s *UtilsSuite) TestIsPhone_NonNumeric() {
	// isNumberString fails on a trailing letter.
	s.T.Expect(utils.IsPhone("0801234567a")).ToEqual(false)
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
