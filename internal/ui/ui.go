package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"atlas.doomwalker/internal/mft"
	"atlas.doomwalker/internal/treemap"
	"github.com/charmbracelet/bubbles/progress"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Palette — Tokyo-Night inspired, designed so adjacent treemap cells read as distinct.
var (
	cBg      = lipgloss.Color("#0d0e12")
	cPanel   = lipgloss.Color("#16181f")
	cBorder  = lipgloss.Color("#30343f")
	cAccent  = lipgloss.Color("#a78bfa")
	cText    = lipgloss.Color("#e5e7eb")
	cMuted   = lipgloss.Color("#6b7280")
	cDim     = lipgloss.Color("#9ca3af")
	cWarn    = lipgloss.Color("#f59e0b")
	cFolder  = lipgloss.Color("#7dd3fc")
	cFile    = lipgloss.Color("#d4d4d8")

	tilePalette = []lipgloss.Color{
		"#7c3aed", "#2563eb", "#0ea5e9", "#06b6d4",
		"#10b981", "#84cc16", "#eab308", "#f59e0b",
		"#f97316", "#ef4444", "#ec4899", "#a855f7",
	}
)

var (
	titleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(cAccent).
			Padding(0, 2).
			Bold(true)

	subtleStyle  = lipgloss.NewStyle().Foreground(cMuted)
	sizeStyle    = lipgloss.NewStyle().Foreground(cWarn).Bold(true)
	helpKeyStyle = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
	helpDescStyle = lipgloss.NewStyle().Foreground(cMuted)

	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(cBorder).
			Background(cPanel)

	rowSelectedStyle = lipgloss.NewStyle().
				Background(cAccent).
				Foreground(lipgloss.Color("#000000")).
				Bold(true)
)

var _ = cText

type sortMode int

const (
	sortBySize sortMode = iota
	sortByName
	sortByCount
)

func (s sortMode) String() string {
	switch s {
	case sortByName:
		return "name"
	case sortByCount:
		return "items"
	default:
		return "size"
	}
}

type Model struct {
	Width, Height int

	Scanning       bool
	Status         string
	Progress       progress.Model
	ProgressAmount float64
	Error          error

	Root         *mft.FileNode
	CurrentNode  *mft.FileNode
	CurrentNodes []*mft.FileNode
	SelectedIndex int
	Sort         sortMode
	ShowHidden   bool

	ConfirmDelete bool
	Toast         string
	ToastIsError  bool

	// Set of node pointers currently being deleted in the background.
	// Members render dimmed and aren't selectable.
	Deleting map[*mft.FileNode]bool

	// Rescan triggers a fresh scan; supplied by main.go.
	Rescan func() tea.Cmd
}

func NewModel() Model {
	p := progress.New(
		progress.WithGradient("#7c3aed", "#06b6d4"),
		progress.WithoutPercentage(),
	)
	return Model{
		Scanning: true,
		Status:   "Initializing",
		Progress: p,
		Sort:     sortBySize,
	}
}

func (m Model) Init() tea.Cmd { return nil }

type ScanFinishedMsg struct{ Root *mft.FileNode }
type ScanErrorMsg struct{ Err error }
type ProgressMsg float64
type StatusMsg string

// DeleteDoneMsg arrives when an async delete finishes.
type DeleteDoneMsg struct {
	Node *mft.FileNode
	Path string
	Err  error
}

func (m *Model) refreshCurrentNodes() {
	m.CurrentNodes = m.CurrentNodes[:0]
	if m.CurrentNode == nil {
		return
	}
	for _, c := range m.CurrentNode.Children {
		if !m.ShowHidden && strings.HasPrefix(c.Name, "$") {
			continue
		}
		m.CurrentNodes = append(m.CurrentNodes, c)
	}
	switch m.Sort {
	case sortByName:
		sort.Slice(m.CurrentNodes, func(i, j int) bool {
			return strings.ToLower(m.CurrentNodes[i].Name) < strings.ToLower(m.CurrentNodes[j].Name)
		})
	case sortByCount:
		sort.Slice(m.CurrentNodes, func(i, j int) bool {
			return len(m.CurrentNodes[i].Children) > len(m.CurrentNodes[j].Children)
		})
	default:
		sort.Slice(m.CurrentNodes, func(i, j int) bool {
			return m.CurrentNodes[i].Size > m.CurrentNodes[j].Size
		})
	}
	if m.SelectedIndex >= len(m.CurrentNodes) {
		m.SelectedIndex = len(m.CurrentNodes) - 1
	}
	if m.SelectedIndex < 0 {
		m.SelectedIndex = 0
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case StatusMsg:
		m.Status = string(msg)
		return m, nil
	case ProgressMsg:
		m.ProgressAmount = float64(msg)
		return m, nil
	case ScanFinishedMsg:
		m.Scanning = false
		m.Error = nil
		m.Root = msg.Root
		// Preserve the user's location across rescans by re-walking the path.
		if m.CurrentNode != nil {
			m.CurrentNode = relocate(msg.Root, m.CurrentNode)
		} else {
			m.CurrentNode = msg.Root
		}
		m.refreshCurrentNodes()
		return m, nil
	case ScanErrorMsg:
		m.Error = msg.Err
		m.Scanning = false
		return m, nil
	case DeleteDoneMsg:
		delete(m.Deleting, msg.Node)
		if msg.Err != nil {
			m.Toast = "Delete failed: " + msg.Err.Error()
			m.ToastIsError = true
			return m, nil
		}
		// Detach from tree and propagate size up.
		if parent := msg.Node.Parent; parent != nil {
			for k, v := range parent.Children {
				if v == msg.Node {
					delete(parent.Children, k)
					break
				}
			}
			removed := msg.Node.Size
			for cur := parent; cur != nil; cur = cur.Parent {
				cur.Size -= removed
			}
		}
		m.refreshCurrentNodes()
		m.Toast = "Deleted " + msg.Path
		m.ToastIsError = false
		return m, nil
	case tea.WindowSizeMsg:
		m.Width = msg.Width
		m.Height = msg.Height
		return m, nil
	case tea.KeyMsg:
		if m.Scanning || m.Error != nil {
			switch msg.String() {
			case "q", "ctrl+c", "esc":
				return m, tea.Quit
			}
			return m, nil
		}
		// Confirmation modal owns key handling.
		if m.ConfirmDelete {
			switch msg.String() {
			case "y", "Y", "enter":
				m.ConfirmDelete = false
				return m, m.beginDelete()
			case "n", "N", "esc", "q", "ctrl+c":
				m.ConfirmDelete = false
				m.Toast = ""
			}
			return m, nil
		}
		// Any keypress dismisses the toast.
		if m.Toast != "" {
			m.Toast = ""
		}
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "o":
			m.openInExplorer()
			return m, nil
		case "d":
			if sel := m.selected(); sel != nil && sel != m.Root && !m.Deleting[sel] {
				m.ConfirmDelete = true
			}
			return m, nil
		case "r":
			if m.Rescan != nil {
				m.Scanning = true
				m.Status = "Rescanning"
				m.ProgressAmount = 0
				m.Toast = ""
				return m, m.Rescan()
			}
			return m, nil
		case "up", "k":
			if m.SelectedIndex > 0 {
				m.SelectedIndex--
			}
		case "down", "j":
			if m.SelectedIndex < len(m.CurrentNodes)-1 {
				m.SelectedIndex++
			}
		case "home", "g":
			m.SelectedIndex = 0
		case "end", "G":
			m.SelectedIndex = len(m.CurrentNodes) - 1
		case "pgup":
			m.SelectedIndex -= 10
			if m.SelectedIndex < 0 {
				m.SelectedIndex = 0
			}
		case "pgdown":
			m.SelectedIndex += 10
			if m.SelectedIndex > len(m.CurrentNodes)-1 {
				m.SelectedIndex = len(m.CurrentNodes) - 1
			}
		case "enter", "right", "l":
			if m.SelectedIndex >= 0 && m.SelectedIndex < len(m.CurrentNodes) {
				sel := m.CurrentNodes[m.SelectedIndex]
				if sel.IsDir && len(sel.Children) > 0 {
					m.CurrentNode = sel
					m.SelectedIndex = 0
					m.refreshCurrentNodes()
				}
			}
		case "backspace", "left", "h":
			if m.CurrentNode != nil && m.CurrentNode.Parent != nil {
				m.CurrentNode = m.CurrentNode.Parent
				m.SelectedIndex = 0
				m.refreshCurrentNodes()
			}
		case "s":
			m.Sort = (m.Sort + 1) % 3
			m.refreshCurrentNodes()
		case ".":
			m.ShowHidden = !m.ShowHidden
			m.refreshCurrentNodes()
		}
		return m, nil
	}
	return m, nil
}

func (m Model) View() string {
	if m.Error != nil {
		return m.renderError()
	}
	if m.Scanning {
		return m.renderScanning()
	}
	return m.renderMain()
}

func (m Model) renderError() string {
	body := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#fca5a5")).
		Bold(true).
		Render("✖ Scan failed") + "\n\n" +
		lipgloss.NewStyle().Foreground(cText).Render(m.Error.Error()) + "\n\n" +
		subtleStyle.Render("Press q to quit.")
	return lipgloss.Place(m.Width, m.Height, lipgloss.Center, lipgloss.Center,
		panelStyle.Padding(1, 3).Render(body))
}

func (m Model) renderScanning() string {
	header := titleStyle.Render(" ATLAS · DOOMWALKER ")
	bar := m.Progress.ViewAs(m.ProgressAmount)
	pct := fmt.Sprintf("%5.1f%%", m.ProgressAmount*100)

	body := lipgloss.JoinVertical(lipgloss.Left,
		header,
		"",
		lipgloss.NewStyle().Foreground(cDim).Render(m.Status+"…"),
		"",
		bar+"  "+lipgloss.NewStyle().Foreground(cAccent).Bold(true).Render(pct),
		"",
		subtleStyle.Render("Reading the Master File Table directly. Press q to abort."),
	)
	return lipgloss.Place(m.Width, m.Height, lipgloss.Center, lipgloss.Center,
		panelStyle.Padding(2, 4).Width(60).Render(body))
}

func (m Model) renderMain() string {
	if m.CurrentNode == nil {
		return "no data"
	}
	w, h := m.Width, m.Height
	if w < 60 || h < 16 {
		return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center,
			subtleStyle.Render("Terminal too small. Resize to at least 60×16."))
	}

	header := m.renderHeader(w)
	footer := m.renderFooter(w)
	innerH := h - lipgloss.Height(header) - lipgloss.Height(footer) - 1

	listW := 42
	if w < 100 {
		listW = w / 3
	}
	mapW := w - listW - 1

	mapPanel := m.renderTreemap(mapW, innerH)
	listPanel := m.renderList(listW, innerH)

	body := lipgloss.JoinHorizontal(lipgloss.Top, mapPanel, " ", listPanel)
	view := header + "\n" + body + "\n" + footer

	if m.ConfirmDelete {
		view = overlay(view, m.renderConfirm(), w, h)
	} else if m.Toast != "" {
		view = overlay(view, m.renderToast(), w, h)
	}
	return view
}

func (m Model) renderConfirm() string {
	sel := m.CurrentNodes[m.SelectedIndex]
	path := nodePath(sel)
	kind := "file"
	if sel.IsDir {
		kind = "directory (recursive)"
	}
	warn := lipgloss.NewStyle().Foreground(lipgloss.Color("#fca5a5")).Bold(true).Render("⚠  Delete " + kind)
	body := lipgloss.NewStyle().Foreground(cText).Render(truncMiddle(path, 70)) + "\n" +
		subtleStyle.Render(FormatSize(sel.Size)+" — this cannot be undone")
	keys := helpKeyStyle.Render("y") + helpDescStyle.Render(" delete  ") +
		helpKeyStyle.Render("n") + helpDescStyle.Render(" cancel")
	content := warn + "\n\n" + body + "\n\n" + keys
	return panelStyle.
		BorderForeground(lipgloss.Color("#ef4444")).
		Padding(1, 3).
		Render(content)
}

func (m Model) renderToast() string {
	fg := lipgloss.Color("#86efac")
	prefix := "✓"
	if m.ToastIsError {
		fg = lipgloss.Color("#fca5a5")
		prefix = "✖"
	}
	return panelStyle.
		BorderForeground(fg).
		Padding(0, 2).
		Render(lipgloss.NewStyle().Foreground(fg).Bold(true).Render(prefix) + " " +
			lipgloss.NewStyle().Foreground(cText).Render(m.Toast))
}

// overlay centers `top` on top of `base`. Lipgloss doesn't have true overlays,
// so we recompose by replacing the matching slice of lines.
func overlay(base, top string, w, h int) string {
	tw := lipgloss.Width(top)
	th := lipgloss.Height(top)
	if tw > w {
		tw = w
	}
	if th > h {
		th = h
	}
	x := (w - tw) / 2
	y := (h - th) / 2

	baseLines := strings.Split(base, "\n")
	topLines := strings.Split(top, "\n")
	for i, tl := range topLines {
		row := y + i
		if row < 0 || row >= len(baseLines) {
			continue
		}
		baseLines[row] = spliceLine(baseLines[row], tl, x, w)
	}
	return strings.Join(baseLines, "\n")
}

// spliceLine replaces a region of `dst` (a styled, ANSI-bearing line) starting
// at visual column x with `src`. We don't try to slice through ANSI sequences;
// instead we re-emit a left padding, the overlay, and a clean reset, which is
// good enough for a centered modal.
func spliceLine(dst, src string, x, totalW int) string {
	pad := strings.Repeat(" ", x)
	return pad + src + lipgloss.NewStyle().Render("")
}

func (m Model) renderHeader(w int) string {
	title := titleStyle.Render(" ATLAS · DOOMWALKER ")
	sizeStr := sizeStyle.Render(FormatSize(m.CurrentNode.Size))
	countStr := subtleStyle.Render(fmt.Sprintf("(%d items)", len(m.CurrentNodes)))
	right := sizeStr + " " + countStr

	titleW := lipgloss.Width(title)
	rightW := lipgloss.Width(right)

	pathBudget := w - titleW - rightW - 4
	if pathBudget < 4 {
		pathBudget = 4
	}
	pathRaw := truncMiddle(nodePath(m.CurrentNode), pathBudget)
	pathW := len(pathRaw)
	pathStyled := lipgloss.NewStyle().Foreground(cFolder).Bold(true).Render(pathRaw)

	pad := w - titleW - 2 - pathW - rightW - 1
	if pad < 1 {
		pad = 1
	}
	return title + "  " + pathStyled + strings.Repeat(" ", pad) + right
}

func (m Model) renderFooter(w int) string {
	keys := []struct{ k, d string }{
		{"↑↓", "select"},
		{"enter", "open"},
		{"bksp", "back"},
		{"o", "explorer"},
		{"d", "delete"},
		{"r", "refresh"},
		{"s", "sort:" + m.Sort.String()},
		{".", "hidden:" + onOff(m.ShowHidden)},
		{"q", "quit"},
	}
	var parts []string
	for _, k := range keys {
		parts = append(parts, helpKeyStyle.Render(k.k)+" "+helpDescStyle.Render(k.d))
	}
	bar := strings.Join(parts, "  ")
	return lipgloss.NewStyle().
		Foreground(cMuted).
		Width(w).
		Render(bar)
}

// renderTreemap composes the treemap by emitting one styled span per
// horizontal run of same-tile cells. This is ~50× faster than calling
// lipgloss.Render per cell, which mattered when scrolling fast.
func (m Model) renderTreemap(w, h int) string {
	if w < 10 || h < 5 {
		return strings.Repeat(" ", w)
	}
	innerW := w - 2
	innerH := h - 2

	nodes := m.CurrentNodes
	if len(nodes) > 80 {
		nodes = nodes[:80]
	}
	in := make([]treemap.Node, 0, len(nodes))
	for _, n := range nodes {
		in = append(in, treemap.Node{Label: n.Name, Size: n.Size})
	}
	rects := treemap.SquarifiedTreemap(in, 0, 0, innerW, innerH)

	// owner[y][x] = tile index, or -1 for background.
	owner := make([][]int16, innerH)
	for i := range owner {
		owner[i] = make([]int16, innerW)
		for j := range owner[i] {
			owner[i][j] = -1
		}
	}
	for i, r := range rects {
		for dy := 0; dy < r.H; dy++ {
			y := r.Y + dy
			if y < 0 || y >= innerH {
				continue
			}
			row := owner[y]
			x0 := r.X
			x1 := r.X + r.W
			if x0 < 0 {
				x0 = 0
			}
			if x1 > innerW {
				x1 = innerW
			}
			for x := x0; x < x1; x++ {
				row[x] = int16(i)
			}
		}
	}

	// Sparse label overlay: chars[y][x] = rune to draw on top of the fill.
	chars := make(map[int64]rune)
	bold := make(map[int64]bool)
	put := func(x, y int, r rune, b bool) {
		if y < 0 || y >= innerH || x < 0 || x >= innerW {
			return
		}
		chars[int64(y)<<32|int64(x)] = r
		if b {
			bold[int64(y)<<32|int64(x)] = true
		}
	}
	for i, r := range rects {
		if r.W < 6 || r.H < 1 {
			continue
		}
		label := r.Label
		sizeLabel := FormatSize(r.Size)
		two := r.H >= 3 && r.W >= len(label)+2 && r.W >= len(sizeLabel)+2
		drawText := func(text string, lineOffset int, b bool) {
			if len(text) > r.W-1 {
				if r.W-2 < 1 {
					return
				}
				text = text[:r.W-2] + "…"
			}
			y := r.Y + r.H/2 - 1 + lineOffset
			x := r.X + (r.W-len(text))/2
			for j, ch := range text {
				put(x+j, y, ch, b)
			}
		}
		if two {
			drawText(label, 0, true)
			drawText(sizeLabel, 1, false)
		} else {
			txt := label
			if len(txt) > r.W-2 {
				if r.W-3 < 1 {
					continue
				}
				txt = txt[:r.W-3] + "…"
			}
			drawText(txt, 0, true)
		}
		_ = i
	}

	// Pre-build styles once per tile (varying by selection).
	tileStyles := make([]lipgloss.Style, len(rects))
	tileBold := make([]lipgloss.Style, len(rects))
	for i := range rects {
		var st lipgloss.Style
		if i == m.SelectedIndex {
			st = lipgloss.NewStyle().Background(cAccent).Foreground(lipgloss.Color("#000"))
		} else {
			color := tilePalette[i%len(tilePalette)]
			st = lipgloss.NewStyle().Background(color).Foreground(lipgloss.Color("#0a0a0a"))
		}
		tileStyles[i] = st
		tileBold[i] = st.Bold(true)
	}
	bgStyle := lipgloss.NewStyle().Background(cBg)

	// Emit one span per run of same (tileIdx, isBold).
	var b strings.Builder
	for y := 0; y < innerH; y++ {
		runStart := 0
		runOwner := owner[y][0]
		runBold := bold[int64(y)<<32]
		flush := func(end int) {
			seg := make([]rune, 0, end-runStart)
			for x := runStart; x < end; x++ {
				if r, ok := chars[int64(y)<<32|int64(x)]; ok {
					seg = append(seg, r)
				} else {
					seg = append(seg, ' ')
				}
			}
			s := string(seg)
			switch {
			case runOwner < 0:
				b.WriteString(bgStyle.Render(s))
			case runBold:
				b.WriteString(tileBold[runOwner].Render(s))
			default:
				b.WriteString(tileStyles[runOwner].Render(s))
			}
		}
		for x := 1; x < innerW; x++ {
			ob := bold[int64(y)<<32|int64(x)]
			if owner[y][x] != runOwner || ob != runBold {
				flush(x)
				runStart = x
				runOwner = owner[y][x]
				runBold = ob
			}
		}
		flush(innerW)
		b.WriteString("\n")
	}
	// Width/Height in lipgloss set the *inner* content area; the rounded
	// border adds 1 row/col on each side, so subtract 2 to keep the panel
	// total at (w, h).
	return panelStyle.Width(w - 2).Height(h - 2).Render(strings.TrimRight(b.String(), "\n"))
}


func (m Model) renderList(w, h int) string {
	innerW := w - 2
	innerH := h - 2

	header := lipgloss.NewStyle().
		Foreground(cAccent).Bold(true).
		Render(fmt.Sprintf("%-*s %10s", innerW-12, "Name", "Size"))

	rows := []string{header, lipgloss.NewStyle().Foreground(cBorder).Render(strings.Repeat("─", innerW))}

	maxRows := innerH - 2
	start := 0
	if m.SelectedIndex >= maxRows {
		start = m.SelectedIndex - maxRows + 1
	}
	end := start + maxRows
	if end > len(m.CurrentNodes) {
		end = len(m.CurrentNodes)
	}

	for i := start; i < end; i++ {
		n := m.CurrentNodes[i]
		icon := "▸"
		nameColor := cFile
		if n.IsDir {
			icon = "▾"
			nameColor = cFolder
		}
		name := n.Name
		nameW := innerW - 12 - 2
		if nameW < 4 {
			nameW = 4
		}
		if len(name) > nameW {
			name = name[:nameW-1] + "…"
		}
		size := FormatSize(n.Size)
		mark := " "
		if m.Deleting[n] {
			mark = "✗"
			nameColor = cMuted
		}
		line := fmt.Sprintf("%s%s %-*s %10s", mark, icon, nameW-1, name, size)
		switch {
		case m.Deleting[n]:
			rows = append(rows, lipgloss.NewStyle().Foreground(cMuted).Faint(true).Render(line))
		case i == m.SelectedIndex:
			rows = append(rows, rowSelectedStyle.Width(innerW).Render(line))
		default:
			rows = append(rows, lipgloss.NewStyle().Foreground(nameColor).Render(line))
		}
	}
	for len(rows)-2 < maxRows {
		rows = append(rows, "")
	}
	body := strings.Join(rows, "\n")
	return panelStyle.Width(w - 2).Height(h - 2).Render(body)
}

func (m *Model) selected() *mft.FileNode {
	if m.SelectedIndex < 0 || m.SelectedIndex >= len(m.CurrentNodes) {
		return nil
	}
	return m.CurrentNodes[m.SelectedIndex]
}

// nodePath builds an absolute filesystem path. The root's Name is "C:" so we
// stitch the rest with backslashes.
func nodePath(n *mft.FileNode) string {
	if n == nil {
		return ""
	}
	var parts []string
	for cur := n; cur != nil; cur = cur.Parent {
		parts = append([]string{cur.Name}, parts...)
	}
	if len(parts) == 0 {
		return ""
	}
	// parts[0] is like "C:" — join the rest with backslashes.
	if len(parts) == 1 {
		return parts[0] + `\`
	}
	return parts[0] + `\` + filepath.Join(parts[1:]...)
}

func (m *Model) openInExplorer() {
	sel := m.selected()
	target := m.CurrentNode
	if sel != nil {
		target = sel
	}
	path := nodePath(target)
	if path == "" {
		return
	}
	var cmd *exec.Cmd
	if sel != nil && !sel.IsDir {
		// Highlight the file inside its folder.
		cmd = exec.Command("explorer.exe", "/select,", path)
	} else {
		cmd = exec.Command("explorer.exe", path)
	}
	if err := cmd.Start(); err != nil {
		m.Toast = "Explorer failed: " + err.Error()
		m.ToastIsError = true
		return
	}
	m.Toast = "Opened " + path
	m.ToastIsError = false
}

// beginDelete returns a tea.Cmd that performs the delete in a goroutine, so
// large directory removals don't block the event loop.
func (m *Model) beginDelete() tea.Cmd {
	sel := m.selected()
	if sel == nil || sel == m.Root {
		return nil
	}
	if m.Deleting == nil {
		m.Deleting = map[*mft.FileNode]bool{}
	}
	m.Deleting[sel] = true
	m.Toast = "Deleting " + sel.Name + "…"
	m.ToastIsError = false
	path := nodePath(sel)
	isDir := sel.IsDir
	return func() tea.Msg {
		var err error
		if isDir {
			err = os.RemoveAll(path)
		} else {
			err = os.Remove(path)
		}
		return DeleteDoneMsg{Node: sel, Path: path, Err: err}
	}
}

// relocate finds a node in `newRoot` whose name-path matches `target`'s. Used
// after a rescan so the user lands back where they were if the path still
// exists; otherwise returns the new root.
func relocate(newRoot, target *mft.FileNode) *mft.FileNode {
	if newRoot == nil || target == nil {
		return newRoot
	}
	var names []string
	for cur := target; cur != nil && cur.Parent != nil; cur = cur.Parent {
		names = append([]string{cur.Name}, names...)
	}
	cur := newRoot
	for _, n := range names {
		next, ok := cur.Children[n]
		if !ok || !next.IsDir {
			return cur
		}
		cur = next
	}
	return cur
}


func truncMiddle(s string, w int) string {
	if w <= 0 || len(s) <= w {
		return s
	}
	if w < 5 {
		return s[:w]
	}
	half := (w - 1) / 2
	return s[:half] + "…" + s[len(s)-(w-1-half):]
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// FormatSize returns a human-readable size with binary units.
func FormatSize(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(size)/float64(div), "KMGTPE"[exp])
}

