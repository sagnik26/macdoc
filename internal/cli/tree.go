package cli

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/sagnik26/macdoc/internal/engine"
)

// The explorer's folder tree: the folder you opened at the top, its biggest
// items beneath it, and any folder that can be opened in place (→ or enter)
// to show what is inside it, as far down as the scan kept anything. It reads
// the stored scan only; opening a folder never touches the disk.

// treeNode is one row of the tree.
type treeNode struct {
	e      engine.ExploreEntry
	parent *treeNode
	kids   []*treeNode
	open   bool
	loaded bool // kids were read from the scan
	note   bool // an explanatory line, not an item
}

// newTree is the tree of one explored folder: a root whose kids are its
// entries.
func newTree(res engine.ExploreResult) *treeNode {
	root := &treeNode{open: true}
	root.fill(res, nil)
	return root
}

// fill sets n's kids from an explored folder, or says why there are none.
func (n *treeNode) fill(res engine.ExploreResult, err error) {
	n.loaded = true
	n.kids = nil
	switch {
	case err != nil:
		n.kids = []*treeNode{{e: engine.ExploreEntry{Name: "couldn't read it: " + err.Error()}, parent: n, note: true}}
	case res.Bytes == nil || len(res.Entries) == 0:
		n.kids = []*treeNode{{e: engine.ExploreEntry{Name: "nothing inside was kept by the scan"}, parent: n, note: true}}
	default:
		for _, e := range res.Entries {
			n.kids = append(n.kids, &treeNode{e: e, parent: n})
		}
	}
}

// expandable reports whether the row is a folder that can be opened.
func (n *treeNode) expandable() bool { return !n.note && n.e.Kind == "dir" && n.e.Path != "" }

// visible lists the rows that are showing, top to bottom.
func (n *treeNode) visible() []*treeNode {
	var out []*treeNode
	var walk func(*treeNode)
	walk = func(p *treeNode) {
		for _, k := range p.kids {
			out = append(out, k)
			if k.open {
				walk(k)
			}
		}
	}
	walk(n)
	return out
}

// indexOf is the position of target among the visible rows, or 0.
func (n *treeNode) indexOf(target *treeNode) int {
	for i, r := range n.visible() {
		if r == target {
			return i
		}
	}
	return 0
}

// last reports whether the row is the last of its siblings.
func (n *treeNode) last() bool {
	return n.parent == nil || n.parent.kids[len(n.parent.kids)-1] == n
}

// guides draws the branch lines in front of the row, such as "│  ├─ ".
func (n *treeNode) guides() string {
	var b strings.Builder
	var above []string
	for p := n.parent; p != nil && p.parent != nil; p = p.parent {
		if p.last() {
			above = append(above, "   ")
		} else {
			above = append(above, "│  ")
		}
	}
	for i := len(above) - 1; i >= 0; i-- {
		b.WriteString(above[i])
	}
	if n.last() {
		return b.String() + "└─ "
	}
	return b.String() + "├─ "
}

// Column widths of the tree: the size, the share bar and the percentage.
const (
	treeSizeW = 10
	treeBarW  = 12
	treePctW  = 7
)

// treeView draws the visible rows at most w cells wide, with the cursor row
// highlighted.
func (m TuiModel) treeView(w int) string {
	rows := m.treeRoot.visible()
	nameW := max(16, w-treeSizeW-treeBarW-treePctW-6)
	var b strings.Builder
	for i, n := range rows {
		b.WriteString(m.treeRow(n, nameW, i == m.treeCursor) + "\n")
	}
	b.WriteString("\n " + m.t.muted.Render("→ or enter opens a folder · ← closes it · backspace goes up one folder") + "\n")
	return b.String()
}

// treeRow draws one row.
func (m TuiModel) treeRow(n *treeNode, nameW int, selected bool) string {
	t := m.t
	paint := func(s lipgloss.Style, text string) string {
		if selected {
			s = t.selectedRow(s)
		}
		return s.Render(text)
	}
	glyph, name := "· ", n.e.Name
	nameStyle := t.text
	switch {
	case n.note:
		glyph, nameStyle = "  ", t.muted.Italic(true)
	case n.e.Kind == "dir":
		glyph, nameStyle = "▸ ", t.Title
		if n.open {
			glyph = "▾ "
		}
		name += "/"
	case n.e.Kind == "other":
		glyph, nameStyle = "… ", t.muted
	}
	guides := n.guides()
	room := max(4, nameW-lipgloss.Width(guides)-lipgloss.Width(glyph))
	name = Truncate(name, room)
	pad := strings.Repeat(" ", max(0, room-lipgloss.Width(name)))
	cursor := "  "
	if selected {
		cursor = t.accent.Render("▌ ")
		if !t.on {
			cursor = "> "
		}
	}
	line := " " + cursor + paint(t.faint, guides) + paint(t.accent, glyph) + paint(nameStyle, name) + paint(t.text, pad)
	if n.note {
		return line
	}
	filled := max(0, min(treeBarW, int(n.e.Share*treeBarW+0.5)))
	bar := paint(t.accent, strings.Repeat("█", filled)) + paint(t.faint, strings.Repeat("░", treeBarW-filled))
	return line + paint(t.size, padLeft(engine.DecimalBytes(n.e.Bytes), treeSizeW)) + paint(t.text, " ") + bar +
		paint(t.muted, padLeft(fmt.Sprintf("%.1f%%", n.e.Share*100), treePctW))
}

// padLeft right-aligns s in w cells.
func padLeft(s string, w int) string {
	if n := w - lipgloss.Width(s); n > 0 {
		return strings.Repeat(" ", n) + s
	}
	return s
}
