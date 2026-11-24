package tests

import (
	"fmt"
	"math"
	"testing"

	"github.com/sagnik26/macdoc/internal/cli"
)

// luminance is the WCAG relative luminance of a #RRGGBB color.
func luminance(hex string) float64 {
	var r, g, b int
	if _, err := fmt.Sscanf(hex, "#%02x%02x%02x", &r, &g, &b); err != nil {
		panic(hex)
	}
	lin := func(v int) float64 {
		c := float64(v) / 255
		if c <= 0.03928 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
}

// contrast is the WCAG contrast ratio of two #RRGGBB colors.
func contrast(a, b string) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// The palette stays readable on both backgrounds (docs/DESIGN_SYSTEM.md 3.5):
// text on the selected row, muted text and chrome on a typical terminal
// background, and the text on every risk badge.
func TestPaletteContrast(t *testing.T) {
	p := cli.Palette()
	const darkBG, lightBG = "#1E1E1E", "#FFFFFF" // typical dark and light terminal backgrounds
	type check struct {
		name     string
		fg, bg   func(cli.PaletteEntry, bool) string
		min      float64
		fgN, bgN string
	}
	pick := func(name string) func(cli.PaletteEntry, bool) string {
		return func(_ cli.PaletteEntry, dark bool) string {
			e := p[name]
			if dark {
				return e.Dark
			}
			return e.Light
		}
	}
	bg := func(_ cli.PaletteEntry, dark bool) string {
		if dark {
			return darkBG
		}
		return lightBG
	}
	for _, c := range []check{
		{"text on the selected row", pick("text"), pick("selectBg"), 7, "text", "selectBg"},
		{"muted text on the selected row", pick("muted"), pick("selectBg"), 3.5, "muted", "selectBg"},
		{"muted text on the background", pick("muted"), bg, 4.5, "muted", "background"},
		{"brand on the background", pick("brand"), bg, 3, "brand", "background"},
		{"accent on the background", pick("accent"), bg, 3, "accent", "background"},
		{"selection marker on the selected row", pick("selectFg"), pick("selectBg"), 3, "selectFg", "selectBg"},
		{"SAFE on the background", pick("safe"), bg, 3, "safe", "background"},
		{"CAUTION on the background", pick("caution"), bg, 3, "caution", "background"},
		{"RISKY on the background", pick("risky"), bg, 3, "risky", "background"},
		{"error on the background", pick("bad"), bg, 3, "bad", "background"},
		{"badge text on SAFE", pick("onBadge"), pick("safe"), 3, "onBadge", "safe"},
		{"badge text on CAUTION", pick("onBadge"), pick("caution"), 3, "onBadge", "caution"},
		{"badge text on RISKY", pick("onBadge"), pick("risky"), 3, "onBadge", "risky"},
		{"badge text on the brand tab", pick("onBadge"), pick("brand"), 3, "onBadge", "brand"},
		{"title chip text", pick("chipFg"), pick("chipBg"), 4.5, "chipFg", "chipBg"},
		{"selected table row text", pick("rowFg"), pick("rowBg"), 4.5, "rowFg", "rowBg"},
	} {
		for _, dark := range []bool{true, false} {
			got := contrast(c.fg(p[c.fgN], dark), c.bg(p[c.bgN], dark))
			if got < c.min {
				t.Errorf("%s (dark=%v): contrast %.2f, want at least %.1f", c.name, dark, got, c.min)
			}
		}
	}
}
