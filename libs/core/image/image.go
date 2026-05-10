package image

import (
	"fmt"
	"strings"
)

// Variant selects the SVG generation style.
type Variant string

const (
	VariantPixel    Variant = "pixel"
	VariantGradient Variant = "gradient"
	VariantSolid    Variant = "solid"
	VariantShapes   Variant = "shapes"
)

// Options configures SVG generation.
type Options struct {
	Width   int
	Height  int
	Text    string
	Colors  []string // one for solid/pixel, two for gradient (start, end)
	Variant Variant
	// Name is the deterministic seed for Pixel/Shapes variants. When empty,
	// it falls back to Text. Mirrors the TS ImageService.new(name, ...).
	Name string
	// IsSquare, when false on Pixel/Shapes, rounds the mask corners.
	IsSquare bool
}

// Service generates SVG images.
type Service struct{}

// New returns an SVG []byte for the given options.
func (s *Service) New(opts Options) []byte {
	if opts.Width <= 0 {
		opts.Width = 200
	}
	if opts.Height <= 0 {
		opts.Height = 200
	}
	if len(opts.Colors) == 0 {
		opts.Colors = []string{"#4f46e5", "#818cf8"}
	}

	switch opts.Variant {
	case VariantPixel:
		return pixelSVG(opts)
	case VariantGradient:
		return gradientSVG(opts)
	case VariantShapes:
		return shapesSVG(opts)
	default:
		return solidSVG(opts)
	}
}

func solidSVG(o Options) []byte {
	color := o.Colors[0]
	svg := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d">
  <rect width="%d" height="%d" fill="%s"/>
  %s
</svg>`, o.Width, o.Height, o.Width, o.Height, color, textElement(o))
	return []byte(svg)
}

func gradientSVG(o Options) []byte {
	start := o.Colors[0]
	end := start
	if len(o.Colors) > 1 {
		end = o.Colors[1]
	}
	svg := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d">
  <defs>
    <linearGradient id="g" x1="0" y1="0" x2="1" y2="1">
      <stop offset="0%%" stop-color="%s"/>
      <stop offset="100%%" stop-color="%s"/>
    </linearGradient>
  </defs>
  <rect width="%d" height="%d" fill="url(#g)"/>
  %s
</svg>`, o.Width, o.Height, start, end, o.Width, o.Height, textElement(o))
	return []byte(svg)
}

// pixelSVG renders a blocky pixel-art grid using the provided colors.
func pixelSVG(o Options) []byte {
	const cols, rows = 8, 8
	cellW := o.Width / cols
	cellH := o.Height / rows

	var sb strings.Builder
	fmt.Fprintf(&sb, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d">`, o.Width, o.Height)

	colors := o.Colors
	if len(colors) < 2 {
		colors = append(colors, "#818cf8")
	}

	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			color := colors[(r+c)%len(colors)]
			x := c * cellW
			y := r * cellH
			fmt.Fprintf(&sb, `<rect x="%d" y="%d" width="%d" height="%d" fill="%s"/>`,
				x, y, cellW, cellH, color)
		}
	}

	sb.WriteString(textElement(o))
	sb.WriteString(`</svg>`)
	return []byte(sb.String())
}

func textElement(o Options) string {
	if o.Text == "" {
		return ""
	}
	cx := o.Width / 2
	cy := o.Height / 2
	return fmt.Sprintf(
		`<text x="%d" y="%d" font-family="sans-serif" font-size="16" fill="#ffffff" text-anchor="middle" dominant-baseline="central">%s</text>`,
		cx, cy, o.Text,
	)
}

// shapesSVG renders the TS DynamicImageVariantType.Shapes variant: rect + rect
// (rotated) + circle + line, all positioned via deterministic hashCode-derived
// transforms. Mirrors ntx-packages/libs/core/src/services/image.service.ts.
func shapesSVG(o Options) []byte {
	const coreSize = 80
	colors := o.Colors
	if len(colors) == 0 {
		colors = []string{"#92A1C6", "#146A7C", "#F0AB3D", "#C271B4", "#C20D90"}
	}

	name := o.Name
	if name == "" {
		name = o.Text
	}
	num := hashCode(name)
	range_ := len(colors)

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
			Color:      getRandomColor(num+i, colors, range_),
			TranslateX: getUnit(num*(i+1), coreSize/2-(i+17), 1),
			TranslateY: getUnit(num*(i+1), coreSize/2-(i+17), 2),
			Rotate:     getUnit(num*(i+1), 360, 0),
			IsSquare:   getBoolean(num, 2),
		}
	}

	rxAttr := ""
	if !o.IsSquare {
		rxAttr = fmt.Sprintf(` rx="%d"`, coreSize*2)
	}

	id := fmt.Sprintf("mask-%d", num)
	rect1Height := coreSize / 8
	if props[1].IsSquare {
		rect1Height = coreSize
	}

	width, height := o.Width, o.Height
	if width == 0 {
		width = 80
	}
	if height == 0 {
		height = 80
	}

	var sb strings.Builder
	fmt.Fprintf(&sb,
		`<svg viewBox="0 0 %d %d" fill="none" role="img" xmlns="http://www.w3.org/2000/svg" width="%d" height="%d">`,
		coreSize, coreSize, width, height)
	fmt.Fprintf(&sb, `<title>%s</title>`, name)
	fmt.Fprintf(&sb,
		`<mask id="%s" maskUnits="userSpaceOnUse" x="0" y="0" width="%d" height="%d"><rect width="%d" height="%d"%s fill="#FFFFFF"/></mask>`,
		id, coreSize, coreSize, coreSize, coreSize, rxAttr)
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

// hashCode mirrors the TS hashCode(): 32-bit shift-and-XOR hash, abs value.
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

// getDigit returns the n-th decimal digit of number (0-indexed).
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

// getBoolean mirrors TS getBoolean: true when the n-th digit is even.
func getBoolean(number, ntn int) bool {
	return getDigit(number, ntn)%2 == 0
}

// getUnit mirrors TS getUnit: number % range, optionally negated when the
// index-th digit of number is even.
func getUnit(number, range_, index int) int {
	if range_ <= 0 {
		return 0
	}
	value := number % range_
	if index > 0 && getDigit(number, index)%2 == 0 {
		return -value
	}
	return value
}

// getRandomColor mirrors TS getRandomColor: deterministic color pick by index.
func getRandomColor(number int, colors []string, range_ int) string {
	if range_ <= 0 || len(colors) == 0 {
		return "#000000"
	}
	idx := number % range_
	if idx < 0 {
		idx = -idx
	}
	return colors[idx]
}
