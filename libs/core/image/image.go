package image

import (
	"fmt"
	"strings"

	"github.com/thescaffold/gox-packages/libs/core/utils"
)

// Variant selects the SVG generation style. Mirrors TS DynamicImageVariantType
// (common.util.ts): only "pixel" and "shapes" exist — there is no gradient or
// solid variant in the TS ImageService.
type Variant string

const (
	VariantPixel  Variant = "pixel"
	VariantShapes Variant = "shapes"
)

// DefaultColors is the palette used by TS ImageService.new() when no colors are
// supplied.
var DefaultColors = []string{"#92A1C6", "#146A7C", "#F0AB3D", "#C271B4", "#C20D90"}

// Options configures SVG generation. Mirrors the argument list of TS
// ImageService.new(name, variant, colors, size, isSquare).
type Options struct {
	// Name is the deterministic seed (hashCode) and the <title> contents.
	Name string
	// Variant is "pixel" (default) or "shapes".
	Variant Variant
	// Colors is the palette; defaults to DefaultColors when empty.
	Colors []string
	// Size sets the rendered width/height attributes (the coordinate space is
	// always the 80x80 coreSize). Defaults to 80.
	Size int
	// IsSquare, when false, rounds the mask corners (rx = coreSize*2).
	IsSquare bool
}

// coreSize is the fixed SVG coordinate space, matching the TS implementation.
const coreSize = 80

// Service generates deterministic SVG avatars. Stateless — mirrors the NestJS
// ImageService.
type Service struct{}

// New returns the SVG bytes for the given options, mirroring TS
// ImageService.new(). Unknown variants fall back to the pixel variant (the TS
// `new()` default parameter value).
func (s *Service) New(opts Options) []byte {
	if opts.Size <= 0 {
		opts.Size = coreSize
	}
	if len(opts.Colors) == 0 {
		opts.Colors = DefaultColors
	}

	switch opts.Variant {
	case VariantShapes:
		return shapesSVG(opts)
	default:
		return pixelSVG(opts)
	}
}

// pixelRectCoords is the fixed (x, y) placement of the 64 pixel cells, in the
// exact order the TS template literal emits them.
var pixelRectCoords = [64][2]int{
	{0, 0}, {20, 0}, {40, 0}, {60, 0}, {10, 0}, {30, 0}, {50, 0}, {70, 0},
	{0, 10}, {0, 20}, {0, 30}, {0, 40}, {0, 50}, {0, 60}, {0, 70},
	{20, 10}, {20, 20}, {20, 30}, {20, 40}, {20, 50}, {20, 60}, {20, 70},
	{40, 10}, {40, 20}, {40, 30}, {40, 40}, {40, 50}, {40, 60}, {40, 70},
	{60, 10}, {60, 20}, {60, 30}, {60, 40}, {60, 50}, {60, 60}, {60, 70},
	{10, 10}, {10, 20}, {10, 30}, {10, 40}, {10, 50}, {10, 60}, {10, 70},
	{30, 10}, {30, 20}, {30, 30}, {30, 40}, {30, 50}, {30, 60}, {30, 70},
	{50, 10}, {50, 20}, {50, 30}, {50, 40}, {50, 50}, {50, 60}, {50, 70},
	{70, 10}, {70, 20}, {70, 30}, {70, 40}, {70, 50}, {70, 60}, {70, 70},
}

// pixelSVG renders the TS DynamicImageVariantType.Pixel variant: a fixed 8x8
// grid of 10x10 cells, each colored by getRandomColor(numFromName % (i+1), ...).
func pixelSVG(o Options) []byte {
	id := utils.UUID()
	numFromName := hashCode(o.Name)
	rng := len(o.Colors)

	properties := make([]string, 64)
	for i := 0; i < 64; i++ {
		properties[i] = getRandomColor(numFromName%(i+1), o.Colors, rng)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb,
		`<svg viewBox="0 0 %d %d" fill="none" role="img" xmlns="http://www.w3.org/2000/svg" width="%d" height="%d">`,
		coreSize, coreSize, o.Size, o.Size)
	fmt.Fprintf(&sb, `<title>%s</title>`, o.Name)
	fmt.Fprintf(&sb,
		`<mask id="%s" maskUnits="userSpaceOnUse" x="0" y="0" width="%d" height="%d"><rect width="%d" height="%d" rx="%s" fill="#FFFFFF"/></mask>`,
		id, coreSize, coreSize, coreSize, coreSize, maskRx(o.IsSquare))
	fmt.Fprintf(&sb, `<g mask="url(#%s)">`, id)
	for i, c := range pixelRectCoords {
		fmt.Fprintf(&sb, `<rect x="%d" y="%d" width="10" height="10" fill="%s"/>`,
			c[0], c[1], properties[i])
	}
	sb.WriteString(`</g></svg>`)
	return []byte(sb.String())
}

// shapesSVG renders the TS DynamicImageVariantType.Shapes variant: a base rect,
// a rotated rect, a circle and a line, all positioned via deterministic
// hashCode-derived transforms.
func shapesSVG(o Options) []byte {
	id := utils.UUID()
	numFromName := hashCode(o.Name)
	rng := len(o.Colors)

	type prop struct {
		Color      string
		TranslateX int
		TranslateY int
		Rotate     int
		IsSquare   bool
	}
	props := make([]prop, 4)
	for i := 0; i < 4; i++ {
		props[i] = prop{
			Color:      getRandomColor(numFromName+i, o.Colors, rng),
			TranslateX: getUnit(numFromName*(i+1), coreSize/2-(i+17), 1),
			TranslateY: getUnit(numFromName*(i+1), coreSize/2-(i+17), 2),
			Rotate:     getUnit(numFromName*(i+1), 360, 0),
			IsSquare:   getBoolean(numFromName, 2),
		}
	}

	rect1Height := coreSize / 8
	if props[1].IsSquare {
		rect1Height = coreSize
	}

	var sb strings.Builder
	fmt.Fprintf(&sb,
		`<svg viewBox="0 0 %d %d" fill="none" role="img" xmlns="http://www.w3.org/2000/svg" width="%d" height="%d">`,
		coreSize, coreSize, o.Size, o.Size)
	fmt.Fprintf(&sb, `<title>%s</title>`, o.Name)
	fmt.Fprintf(&sb,
		`<mask id="%s" maskUnits="userSpaceOnUse" x="0" y="0" width="%d" height="%d"><rect width="%d" height="%d" rx="%s" fill="#FFFFFF"/></mask>`,
		id, coreSize, coreSize, coreSize, coreSize, maskRx(o.IsSquare))
	fmt.Fprintf(&sb, `<g mask="url(#%s)">`, id)
	fmt.Fprintf(&sb, `<rect width="%d" height="%d" fill="%s"/>`, coreSize, coreSize, props[0].Color)
	fmt.Fprintf(&sb,
		`<rect x="%d" y="%d" width="%d" height="%d" fill="%s" transform="translate(%d %d) rotate(%d %d %d)"/>`,
		(coreSize-60)/2, (coreSize-20)/2, coreSize, rect1Height, props[1].Color,
		props[1].TranslateX, props[1].TranslateY, props[1].Rotate, coreSize/2, coreSize/2)
	fmt.Fprintf(&sb,
		`<circle cx="%d" cy="%d" fill="%s" r="%d" transform="translate(%d %d)"/>`,
		coreSize/2, coreSize/2, props[2].Color, coreSize/5, props[2].TranslateX, props[2].TranslateY)
	fmt.Fprintf(&sb,
		`<line x1="0" y1="%d" x2="%d" y2="%d" strokeWidth="2" stroke="%s" transform="translate(%d %d) rotate(%d %d %d)"/>`,
		coreSize/2, coreSize, coreSize/2, props[3].Color,
		props[3].TranslateX, props[3].TranslateY, props[3].Rotate, coreSize/2, coreSize/2)
	sb.WriteString(`</g></svg>`)
	return []byte(sb.String())
}

// maskRx mirrors the TS `rx="${isSquare ? '' : coreSize * 2}"` expression: an
// empty attribute value when square, coreSize*2 otherwise.
func maskRx(isSquare bool) string {
	if isSquare {
		return ""
	}
	return fmt.Sprintf("%d", coreSize*2)
}

// --- helpers (mirror ntx-packages/libs/core/src/utils/image.util.ts) ---

// hashCode mirrors TS hashCode(): 32-bit shift-and-XOR hash, absolute value.
func hashCode(name string) int {
	var hash int32
	for _, ch := range name {
		hash = (hash << 5) - hash + int32(ch)
	}
	if hash < 0 {
		hash = -hash
	}
	return int(hash)
}

// getDigit mirrors TS getDigit(): the n-th decimal digit of number (0-indexed).
func getDigit(number, ntn int) int {
	return (number / pow10(ntn)) % 10
}

func pow10(n int) int {
	out := 1
	for i := 0; i < n; i++ {
		out *= 10
	}
	return out
}

// getBoolean mirrors TS getBoolean(): true when the n-th digit is even.
func getBoolean(number, ntn int) bool {
	return getDigit(number, ntn)%2 == 0
}

// getUnit mirrors TS getUnit(): number % range, negated when index is truthy
// (> 0) and the index-th digit of number is even.
func getUnit(number, rangeV, index int) int {
	if rangeV == 0 {
		return 0
	}
	value := number % rangeV
	if index > 0 && getDigit(number, index)%2 == 0 {
		return -value
	}
	return value
}

// getRandomColor mirrors TS getRandomColor(): colors[number % range].
func getRandomColor(number int, colors []string, rangeV int) string {
	if rangeV <= 0 || len(colors) == 0 {
		return ""
	}
	return colors[number%rangeV]
}
