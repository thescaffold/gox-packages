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
)

// Options configures SVG generation.
type Options struct {
	Width   int
	Height  int
	Text    string
	Colors  []string // one for solid/pixel, two for gradient (start, end)
	Variant Variant
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
