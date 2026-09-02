package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// navRows is the keypad as the cursor walks it: the four rows of backlit keys,
// then the thumb cluster.
var navRows = func() [][]string {
	rows := make([][]string, 0, len(gridRows)+1)
	rows = append(rows, gridRows...)
	return append(rows, thumbCluster)
}()

type mode int

const (
	modeGrid mode = iota
	modeBind
	modeCapture
)

type editor struct {
	profile *Profile
	parent  *Profile
	dirs    []string
	slugs   []string
	slugAt  int

	row, col int
	mode     mode
	filter   string
	choice   int
	matches  []string

	status string
	dirty  bool
	apply  bool
	width  int
}

// captureNames maps what the terminal reports to a Linux key name. Bare
// modifiers never reach the terminal at all, which is why capture cannot cover
// capslock, leftshift or leftalt: those must be typed.
var captureNames = map[string]string{
	"esc": "esc", "escape": "esc", "enter": "enter", "tab": "tab",
	" ": "space", "space": "space", "backspace": "backspace", "delete": "delete",
	"up": "up", "down": "down", "left": "left", "right": "right",
	"home": "home", "end": "end", "pgup": "pageup", "pgdown": "pagedown",
	"insert": "insert",
	"-":      "minus", "=": "equal", "[": "leftbrace", "]": "rightbrace",
	";": "semicolon", "'": "apostrophe", "`": "grave", "\\": "backslash",
	",": "comma", ".": "dot", "/": "slash",
}

func newEditor(slug string, dirs []string) (*editor, error) {
	profile, err := LoadProfile(slug, dirs)
	if err != nil {
		return nil, err
	}
	e := &editor{
		profile: profile,
		dirs:    dirs,
		slugs:   ListProfiles(dirs),
		status:  "arrow keys move · enter binds · c captures · x unbinds · s saves",
	}
	for i, s := range e.slugs {
		if s == slug {
			e.slugAt = i
		}
	}
	e.loadParent()
	return e, nil
}

func (e *editor) loadParent() {
	e.parent = nil
	if e.profile.Extends == "" {
		return
	}
	if parent, err := LoadProfile(e.profile.Extends, e.dirs); err == nil {
		e.parent = parent
	}
}

func (e *editor) label() string { return navRows[e.row][e.col] }

func (e *editor) Init() tea.Cmd { return nil }

func (e *editor) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		e.width = msg.Width
		return e, nil
	case tea.KeyMsg:
		switch e.mode {
		case modeBind:
			return e.updateBind(msg)
		case modeCapture:
			return e.updateCapture(msg)
		default:
			return e.updateGrid(msg)
		}
	}
	return e, nil
}

func (e *editor) updateGrid(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		if e.dirty {
			e.status = "unsaved changes — press s to save, or Q to discard and quit"
			return e, nil
		}
		return e, tea.Quit
	case "Q":
		return e, tea.Quit
	case "up", "k":
		e.row = clamp(e.row-1, 0, len(navRows)-1)
		e.col = clamp(e.col, 0, len(navRows[e.row])-1)
	case "down", "j":
		e.row = clamp(e.row+1, 0, len(navRows)-1)
		e.col = clamp(e.col, 0, len(navRows[e.row])-1)
	case "left", "h":
		e.col = clamp(e.col-1, 0, len(navRows[e.row])-1)
	case "right", "l":
		e.col = clamp(e.col+1, 0, len(navRows[e.row])-1)
	case "enter", "b":
		e.mode = modeBind
		e.filter = ""
		e.choice = 0
		e.refilter()
		e.status = "type a key name, or a macro like C-c or macro(1 100ms 2)"
	case "c":
		e.mode = modeCapture
		e.status = "press the key you want " + e.label() + " to send"
	case "x", "delete", "backspace":
		e.profile.Bindings[e.label()] = "off"
		e.dirty = true
		e.status = e.label() + " now sends nothing"
	case "s":
		e.save()
	case "a":
		e.save()
		if e.status == "" || strings.HasPrefix(e.status, "saved") {
			e.apply = true
			return e, tea.Quit
		}
	case "tab":
		if len(e.slugs) > 1 && !e.dirty {
			e.slugAt = (e.slugAt + 1) % len(e.slugs)
			if next, err := LoadProfile(e.slugs[e.slugAt], e.dirs); err == nil {
				e.profile = next
				e.loadParent()
				e.status = "switched to " + next.Name
			}
		} else if e.dirty {
			e.status = "save before switching profile"
		}
	}
	return e, nil
}

func (e *editor) updateBind(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		e.mode = modeGrid
		e.status = ""
	case "enter":
		value := strings.TrimSpace(e.filter)
		if !isPlainKey(value) && len(e.matches) > 0 && e.choice < len(e.matches) {
			value = e.matches[e.choice]
		}
		if value == "" {
			e.status = "nothing to bind"
			return e, nil
		}
		e.profile.Bindings[e.label()] = value
		e.dirty = true
		e.status = e.label() + " -> " + value
		e.mode = modeGrid
	case "up":
		e.choice = clamp(e.choice-1, 0, maxInt(len(e.matches)-1, 0))
	case "down":
		e.choice = clamp(e.choice+1, 0, maxInt(len(e.matches)-1, 0))
	case "backspace":
		if e.filter != "" {
			e.filter = e.filter[:len(e.filter)-1]
			e.refilter()
		}
	default:
		if s := msg.String(); len(s) == 1 || s == "space" {
			if s == "space" {
				s = " "
			}
			e.filter += s
			e.refilter()
		}
	}
	return e, nil
}

func (e *editor) updateCapture(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	pressed := msg.String()
	if pressed == "esc" {
		e.mode = modeGrid
		e.status = "capture cancelled"
		return e, nil
	}
	name, ok := captureNames[pressed]
	if !ok {
		if isPlainKey(pressed) {
			name = pressed
		} else {
			e.status = fmt.Sprintf("cannot capture %q — type it with enter instead", pressed)
			e.mode = modeGrid
			return e, nil
		}
	}
	e.profile.Bindings[e.label()] = name
	e.dirty = true
	e.status = e.label() + " -> " + name + " (captured)"
	e.mode = modeGrid
	return e, nil
}

func (e *editor) refilter() {
	e.matches = nil
	needle := strings.ToLower(strings.TrimSpace(e.filter))
	for _, name := range sortedKeyNames {
		if needle == "" || strings.Contains(name, needle) {
			e.matches = append(e.matches, name)
		}
	}
	sort.SliceStable(e.matches, func(i, j int) bool {
		return strings.HasPrefix(e.matches[i], needle) && !strings.HasPrefix(e.matches[j], needle)
	})
	e.choice = 0
}

func (e *editor) save() {
	path, err := findProfile(e.profile.Slug, e.dirs)
	if err != nil {
		path = filepath.Join(e.dirs[0], e.profile.Slug+profileSuffix)
	}
	if err := e.profile.Save(path, e.parent); err != nil {
		e.status = "save failed: " + err.Error()
		return
	}
	e.dirty = false
	e.status = "saved " + path
}

// ---- rendering ----

var (
	capLit = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("180")).
		Foreground(lipgloss.Color("180")).Bold(true).
		Width(9).Align(lipgloss.Center)
	capOff = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("238")).
		Foreground(lipgloss.Color("240")).
		Width(9).Align(lipgloss.Center)
	capCursor = lipgloss.NewStyle().
			Border(lipgloss.DoubleBorder()).BorderForeground(lipgloss.Color("111")).
			Foreground(lipgloss.Color("231")).Bold(true).
			Width(9).Align(lipgloss.Center)
	labelStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("242"))
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231"))
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	accentStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("180"))
	warnStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("210"))
	pickStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("231")).Background(lipgloss.Color("60"))
)

func (e *editor) cap(label string) string {
	value, bound := e.profile.Bindings[label]
	face := "·"
	if bound && !isOff(value) {
		face = value
		if strings.HasPrefix(value, "macro(") {
			face = "macro"
		}
		if len(face) > 7 {
			face = face[:6] + "…"
		}
	}
	body := labelStyle.Render(label) + "\n" + face
	switch {
	case label == e.label():
		return capCursor.Render(body)
	case bound && !isOff(value):
		return capLit.Render(body)
	default:
		return capOff.Render(body)
	}
}

func (e *editor) View() string {
	var b strings.Builder

	head := titleStyle.Render("Razer Tartarus V2") + dimStyle.Render(" — ") +
		accentStyle.Render(e.profile.Name)
	if e.profile.Extends != "" {
		head += dimStyle.Render("  extends " + e.profile.Extends)
	}
	if e.dirty {
		head += warnStyle.Render("  ● unsaved")
	}
	b.WriteString(head + "\n\n")

	for _, row := range gridRows {
		caps := make([]string, 0, len(row))
		for _, label := range row {
			caps = append(caps, e.cap(label))
		}
		b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, caps...) + "\n")
	}
	b.WriteString("\n")

	thumbs := make([]string, 0, len(thumbCluster))
	for _, label := range thumbCluster {
		thumbs = append(thumbs, e.cap(label))
	}
	b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, thumbs...) + "\n\n")

	switch e.mode {
	case modeBind:
		b.WriteString(accentStyle.Render("bind "+e.label()+" → ") + e.filter + "▏\n")
		shown := e.matches
		if len(shown) > 8 {
			shown = shown[:8]
		}
		for i, name := range shown {
			line := fmt.Sprintf("  %-16s %3d", name, keyboardKeyCodes[name])
			if i == e.choice {
				b.WriteString(pickStyle.Render(line) + "\n")
			} else {
				b.WriteString(dimStyle.Render(line) + "\n")
			}
		}
		if len(e.matches) > 8 {
			b.WriteString(dimStyle.Render(fmt.Sprintf("  … %d more of 138\n", len(e.matches)-8)))
		}
		if len(e.matches) == 0 {
			b.WriteString(dimStyle.Render("  no key matches — enter binds it verbatim as a macro\n"))
		}
	case modeCapture:
		b.WriteString(accentStyle.Render("capture: press the key "+e.label()+" should send") + "\n")
		b.WriteString(dimStyle.Render("  bare modifiers never reach the terminal — type those instead\n"))
	default:
		b.WriteString(dimStyle.Render(
			"↑↓←→ move   enter bind   c capture   x unbind   s save   a save+apply   tab profile   q quit") + "\n")
	}

	if e.status != "" {
		b.WriteString("\n" + dimStyle.Render(e.status) + "\n")
	}
	return b.String()
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// RunEditor opens the keypad editor. It reports whether the caller should apply
// the profile, which happens after the alt-screen is torn down so sudo owns the
// terminal cleanly.
func RunEditor(slug string, dirs []string) (applySlug string, err error) {
	e, err := newEditor(slug, dirs)
	if err != nil {
		return "", err
	}
	final, err := tea.NewProgram(e, tea.WithAltScreen()).Run()
	if err != nil {
		return "", err
	}
	done := final.(*editor)
	if done.apply {
		return done.profile.Slug, nil
	}
	return "", nil
}
