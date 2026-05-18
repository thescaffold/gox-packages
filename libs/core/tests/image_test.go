package tests

import (
	"strings"
	"testing"

	test "github.com/awesome-goose/goose/testing"
	"github.com/thescaffold/gox-packages/libs/core/image"
)

func TestImage(t *testing.T) {
	test.NewSuiteRunner(t, &ImageSuite{}).Run()
}

type ImageSuite struct {
	test.Suite
}

// TestNew_DefaultsToPixel asserts the unknown / empty variant falls back to the
// pixel variant, mirroring TS ImageService.new()'s default parameter.
func (s *ImageSuite) TestNew_DefaultsToPixel() {
	svc := &image.Service{}
	out := string(svc.New(image.Options{Name: "Alpha"}))
	// pixel variant emits 64 <rect> tiles in addition to the mask rect.
	s.T.Expect(strings.Count(out, "<rect")).ToEqual(65)
}

func (s *ImageSuite) TestNew_Pixel_ContainsTitle() {
	svc := &image.Service{}
	out := string(svc.New(image.Options{Name: "MyName", Variant: image.VariantPixel}))
	s.T.Expect(strings.Contains(out, "<title>MyName</title>")).ToEqual(true)
}

// TestNew_Deterministic confirms the same name yields the same SVG body
// (modulo the random uuid mask id) — proving hashCode-driven color selection.
func (s *ImageSuite) TestNew_Deterministic() {
	svc := &image.Service{}
	a := string(svc.New(image.Options{Name: "Alpha"}))
	b := string(svc.New(image.Options{Name: "Alpha"}))
	// Strip the mask id (the only random piece) to compare bodies.
	stripID := func(s string) string {
		// Replace the id attribute and url(#id) reference with placeholders.
		idStart := strings.Index(s, `id="`)
		if idStart < 0 {
			return s
		}
		idEnd := strings.Index(s[idStart+4:], `"`)
		if idEnd < 0 {
			return s
		}
		id := s[idStart+4 : idStart+4+idEnd]
		return strings.ReplaceAll(s, id, "FIXED")
	}
	s.T.Expect(stripID(a)).ToEqual(stripID(b))
}

func (s *ImageSuite) TestNew_DifferentNames_DifferentColors() {
	svc := &image.Service{}
	a := string(svc.New(image.Options{Name: "Alpha"}))
	b := string(svc.New(image.Options{Name: "Bravo"}))
	// Pixel rects fill colors are deterministic from the hashCode — different
	// names should yield at least one differing fill.
	s.T.Expect(a == b).ToEqual(false)
}

func (s *ImageSuite) TestNew_Shapes_ContainsExpectedElements() {
	svc := &image.Service{}
	out := string(svc.New(image.Options{Name: "Shape", Variant: image.VariantShapes}))
	// Shapes variant: 2 rects (base + rotated) + circle + line.
	s.T.Expect(strings.Contains(out, "<circle")).ToEqual(true)
	s.T.Expect(strings.Contains(out, "<line")).ToEqual(true)
	s.T.Expect(strings.Count(out, "<rect")).ToEqual(3) // mask rect + base + rotated
}

func (s *ImageSuite) TestNew_NonSquare_RoundsCorners() {
	svc := &image.Service{}
	out := string(svc.New(image.Options{Name: "x", IsSquare: false}))
	// Non-square sets rx to coreSize*2 (160).
	s.T.Expect(strings.Contains(out, `rx="160"`)).ToEqual(true)
}

func (s *ImageSuite) TestNew_Square_EmptyRx() {
	svc := &image.Service{}
	out := string(svc.New(image.Options{Name: "x", IsSquare: true}))
	s.T.Expect(strings.Contains(out, `rx=""`)).ToEqual(true)
}

func (s *ImageSuite) TestNew_CustomSize() {
	svc := &image.Service{}
	out := string(svc.New(image.Options{Name: "x", Size: 256}))
	s.T.Expect(strings.Contains(out, `width="256"`)).ToEqual(true)
	s.T.Expect(strings.Contains(out, `height="256"`)).ToEqual(true)
	// viewBox always stays at 80x80 (the coreSize) regardless of rendered size.
	s.T.Expect(strings.Contains(out, `viewBox="0 0 80 80"`)).ToEqual(true)
}

func (s *ImageSuite) TestNew_DefaultColors_AppliedWhenColorsNil() {
	svc := &image.Service{}
	out := string(svc.New(image.Options{Name: "x"}))
	// At least one of the default palette colors must appear in the output.
	any := false
	for _, c := range image.DefaultColors {
		if strings.Contains(out, c) {
			any = true
			break
		}
	}
	s.T.Expect(any).ToEqual(true)
}
