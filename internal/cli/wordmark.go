package cli

import "charm.land/lipgloss/v2"

// The welcome screen's wordmark: "macdoc" in chunky letters, eight terminal rows
// tall, with a cat in the place of the O. The cat is drawn in the style of the
// Bubbles README header (a round violet head, pink inner ears, upright dark
// eyes, pink cheeks and whiskers, a small coral smile) and wears a doctor's
// stethoscope, because macdoc is the doctor for your Mac.
//
// Everything is drawn in half blocks: a terminal cell holds two pixels, the
// upper one as the foreground of "▀" and the lower one as its background.

// wordmarkHeight is the height of the wordmark in pixels (two per terminal row).
const wordmarkHeight = 16

// wordmarkGap is the space between two glyphs, in pixels.
const wordmarkGap = 2

// catPalette maps the letters of catGlyph to colors. The letters of the word
// itself use the brand gradient, one color per pixel row.
var catPalette = map[byte]string{
	'a': "#8D87FF", // head highlight
	'c': "#7571F9", // head
	'd': "#5F5AE6", // head shade and the outside of the ears
	'e': "#FF9ADA", // inside of the ears
	'f': "#1B1B3A", // eyes
	'g': "#FF9AE0", // cheeks and whiskers
	'h': "#FF6B6B", // smile
	'j': "#C9D3E8", // stethoscope tube
	'k': "#8F9BB8", // tube shadow
	'l': "#5CFFD0", // earpieces and the rim of the chest piece
	'm': "#D7DEEF", // chest piece
	'n': "#8895B0", // chest piece center
}

// The glyphs, one string per pixel row: "#" is a pixel of the word, "." is
// transparent, and the cat uses the letters of catPalette.
var glyphM = []string{
	".####......##.",
	"#####......###",
	"######....####",
	"######....####",
	"###.###..#####",
	"###..#########",
	"###..#########",
	"###...####.###",
	"###...####.###",
	"###........###",
	"###........###",
	"###........###",
	"###........###",
	"###........###",
	"###........###",
	"###........###",
}

var glyphA = []string{
	"...####...",
	"..######..",
	".########.",
	"###....###",
	"###....###",
	"###....###",
	"###....###",
	"###....###",
	"##########",
	"##########",
	"##########",
	"###....###",
	"###....###",
	"###....###",
	"###....###",
	"###....###",
}

var glyphC = []string{
	"..#####..",
	".#######.",
	"#########",
	"###...###",
	"###...###",
	"###......",
	"###......",
	"###......",
	"###......",
	"###......",
	"###......",
	"###...###",
	"###...###",
	"#########",
	".#######.",
	"..#####..",
}

var glyphD = []string{
	"#######...",
	"########..",
	"#########.",
	"###....###",
	"###....###",
	"###....###",
	"###....###",
	"###....###",
	"###....###",
	"###....###",
	"###....###",
	"###....###",
	"###....###",
	"#########.",
	"########..",
	"#######...",
}

var glyphCat = []string{
	"...d..........d...",
	"...dd........dd...",
	"...ddaaacccccdd...",
	"..ddaaaaacccccdd..",
	"..aaaaaaaccccccc..",
	"..aaafaaacccfccc..",
	"..ccafacccccfccc..",
	"g.cccfccccccfccd.g",
	".gcggcchcchccggdg.",
	"g..ccccchhcccdd..g",
	"....cccccccddd....",
	"...l...dddd...l...",
	"...jj........jj...",
	"....jjj....jjj....",
	".......jmmj.......",
	"........ll........",
}

// wordmarkGlyphs is "macdoc" with the cat in the place of the O.
var wordmarkGlyphs = [][]string{glyphM, glyphA, glyphC, glyphD, glyphCat, glyphC}

// wordmarkWidth is the width of the wordmark in terminal columns.
func wordmarkWidth() int {
	w := wordmarkGap * (len(wordmarkGlyphs) - 1)
	for _, g := range wordmarkGlyphs {
		w += len(g[0])
	}
	return w
}

// wordmark returns the wordmark as lines of styled text, or nil when the theme
// draws no color (the decoration is for color terminals only).
func wordmark(t *Theme) []string {
	if !t.color {
		return nil
	}
	grad := gradient(wordmarkHeight)
	pix := make([][]string, wordmarkHeight) // a color per pixel, "" for transparent
	for y := range pix {
		for gi, g := range wordmarkGlyphs {
			if gi > 0 {
				pix[y] = append(pix[y], make([]string, wordmarkGap)...)
			}
			for x := 0; x < len(g[y]); x++ {
				switch c := g[y][x]; c {
				case '.':
					pix[y] = append(pix[y], "")
				case '#':
					pix[y] = append(pix[y], grad[y])
				default:
					pix[y] = append(pix[y], catPalette[c])
				}
			}
		}
	}
	var lines []string
	for y := 0; y < wordmarkHeight; y += 2 {
		var b []byte
		for x := range pix[y] {
			top, bottom := pix[y][x], ""
			if y+1 < len(pix) {
				bottom = pix[y+1][x]
			}
			st := lipgloss.NewStyle()
			switch {
			case top != "" && bottom != "":
				b = append(b, st.Foreground(lipgloss.Color(top)).Background(lipgloss.Color(bottom)).Render("▀")...)
			case top != "":
				b = append(b, st.Foreground(lipgloss.Color(top)).Render("▀")...)
			case bottom != "":
				b = append(b, st.Foreground(lipgloss.Color(bottom)).Render("▄")...)
			default:
				b = append(b, ' ')
			}
		}
		lines = append(lines, string(b))
	}
	return lines
}
