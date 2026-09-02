package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Profile is one game's worth of bindings, with any inherited profile already
// applied underneath it.
type Profile struct {
	Slug        string
	Name        string
	Device      string
	Extends     string
	Bindings    map[string]string // physical label -> key name, macro, or "off"
	Macros      map[string]string // named macro -> keyd macro body
	Order       []string          // the order [macros] were declared, for stable output
	Passthrough bool              // leave unnamed keys at their stock function
}

const profileSuffix = ".profile"

// offWords are the ways a profile says "this key sends nothing". A literal "0"
// is deliberately absent: it is the digit zero, which is what the .map files
// got wrong.
var offWords = map[string]bool{
	"off": true, "none": true, "-": true, "": true,
	"noop": true, "reserved": true, "unbound": true, "disabled": true,
}

func isOff(value string) bool { return offWords[strings.ToLower(strings.TrimSpace(value))] }

// keyboardKeyCodes is the key name -> Linux keycode table, parsed once.
var keyboardKeyCodes = func() map[string]int {
	table := make(map[string]int)
	for _, pair := range strings.Fields(keyboardKeys) {
		name, code, found := strings.Cut(pair, ":")
		if !found {
			continue
		}
		if n, err := strconv.Atoi(code); err == nil {
			table[name] = n
		}
	}
	return table
}()

func isPlainKey(value string) bool {
	_, ok := keyboardKeyCodes[strings.ToLower(value)]
	return ok
}

// sortedKeyNames is every bindable key name, ordered by keycode so the list
// reads like a keyboard rather than an alphabet.
var sortedKeyNames = func() []string {
	names := make([]string, 0, len(keyboardKeyCodes))
	for name := range keyboardKeyCodes {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		return keyboardKeyCodes[names[i]] < keyboardKeyCodes[names[j]]
	})
	return names
}()

// aliases are friendlier names a profile may use for the thumb cluster.
var aliases = map[string]string{
	"thumb_key": "thumb", "k20": "thumb",
	"spacebar": "bar", "thumb_bar": "bar",
	"pad_u": "pad_up", "pad_d": "pad_down", "pad_l": "pad_left", "pad_r": "pad_right",
}

var inputByLabel = func() map[string]Input {
	byLabel := make(map[string]Input, len(layout))
	for _, in := range layout {
		byLabel[in.Label] = in
	}
	return byLabel
}()

func canonicalLabel(raw string) (string, error) {
	label := strings.ToLower(strings.TrimSpace(raw))
	if mapped, ok := aliases[label]; ok {
		label = mapped
	}
	if _, ok := inputByLabel[label]; !ok {
		return "", fmt.Errorf("unknown key %q", raw)
	}
	return label, nil
}

// System directories for profiles. The package ships read-only defaults under
// /usr/share; /etc is left for an administrator to override them.
const (
	SystemProfileDir = "/usr/share/tartarus/profiles"
	AdminProfileDir  = "/etc/tartarus/profiles"
)

// ProfileDirs are the directories searched for profiles, most specific first.
// A profile in an earlier directory shadows one of the same name later, so a
// user's own copy always beats the packaged default.
func ProfileDirs(override string) []string {
	if override != "" {
		return []string{override}
	}
	var dirs []string
	if env := os.Getenv("TARTARUS_PROFILES"); env != "" {
		dirs = append(dirs, env)
	}
	config := os.Getenv("XDG_CONFIG_HOME")
	if config == "" {
		if home, err := os.UserHomeDir(); err == nil {
			config = filepath.Join(home, ".config")
		}
	}
	if config != "" {
		dirs = append(dirs, filepath.Join(config, "tartarus", "profiles"))
		dirs = append(dirs, filepath.Join(config, "tartarus"))
	}
	// Running from a build tree, before anything is installed. An installed
	// binary sits in a bin directory, where a profiles/ subdirectory would be
	// nonsense, so it is not looked for there.
	if exe, err := os.Executable(); err == nil {
		if dir := filepath.Dir(exe); !isBinDir(dir) {
			dirs = append(dirs, filepath.Join(dir, "profiles"))
		}
	}
	dirs = append(dirs, AdminProfileDir, SystemProfileDir, "profiles")
	return dedupe(dirs)
}

// isBinDir reports whether a directory is one binaries are installed into.
func isBinDir(dir string) bool {
	switch filepath.Clean(dir) {
	case "/bin", "/sbin", "/usr/bin", "/usr/sbin", "/usr/local/bin", "/usr/local/sbin":
		return true
	}
	return false
}

// dedupe drops repeated directories, which happen easily: running from a build
// tree with TARTARUS_PROFILES pointing at it names the same place twice.
func dedupe(dirs []string) []string {
	seen := make(map[string]bool, len(dirs))
	out := dirs[:0:0]
	for _, dir := range dirs {
		key := dir
		if abs, err := filepath.Abs(dir); err == nil {
			key = abs
		}
		if !seen[key] {
			seen[key] = true
			out = append(out, dir)
		}
	}
	return out
}

// UserProfileDir is where a profile is written when no directory was named.
// Profiles belong to the person using the keypad, not to the machine.
func UserProfileDir() (string, error) {
	if config := os.Getenv("XDG_CONFIG_HOME"); config != "" {
		return filepath.Join(config, "tartarus", "profiles"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot locate your home directory: %w", err)
	}
	return filepath.Join(home, ".config", "tartarus", "profiles"), nil
}

// ListProfiles returns the slugs found across dirs, without duplicates.
func ListProfiles(dirs []string) []string {
	var slugs []string
	seen := map[string]bool{}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), profileSuffix) {
				continue
			}
			slug := strings.TrimSuffix(entry.Name(), profileSuffix)
			if !seen[slug] {
				seen[slug] = true
				slugs = append(slugs, slug)
			}
		}
	}
	sort.Strings(slugs)
	return slugs
}

func findProfile(slug string, dirs []string) (string, error) {
	for _, dir := range dirs {
		path := filepath.Join(dir, slug+profileSuffix)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, nil
		}
	}
	return "", fmt.Errorf("no profile named %q (searched: %s)", slug, strings.Join(dirs, ", "))
}

// LoadProfile reads a profile and layers it on top of any profile it extends.
func LoadProfile(slug string, dirs []string) (*Profile, error) {
	return loadProfile(slug, dirs, nil)
}

func loadProfile(slug string, dirs []string, chain []string) (*Profile, error) {
	for _, seen := range chain {
		if seen == slug {
			return nil, fmt.Errorf("profile inheritance loops: %s", strings.Join(append(chain, slug), " -> "))
		}
	}
	path, err := findProfile(slug, dirs)
	if err != nil {
		return nil, err
	}
	sections, order, err := parseINI(path)
	if err != nil {
		return nil, err
	}

	meta := sections["profile"]
	parent := strings.TrimSpace(meta["extends"])

	var profile *Profile
	if parent != "" {
		profile, err = loadProfile(parent, dirs, append(chain, slug))
		if err != nil {
			return nil, err
		}
		profile.Slug = slug
	} else {
		profile = &Profile{
			Slug:     slug,
			Name:     slug,
			Device:   defaultDevice,
			Bindings: map[string]string{},
			Macros:   map[string]string{},
		}
	}
	profile.Extends = parent

	if name := strings.TrimSpace(meta["name"]); name != "" {
		profile.Name = name
	}
	if device := strings.TrimSpace(meta["device"]); device != "" {
		profile.Device = strings.ToLower(device)
	}
	switch strings.ToLower(strings.TrimSpace(meta["unbound"])) {
	case "passthrough":
		profile.Passthrough = true
	case "off":
		profile.Passthrough = false
	case "":
	default:
		return nil, fmt.Errorf("%s: unbound must be 'off' or 'passthrough'", path)
	}

	// Declaration order, not map order: Go randomises map iteration, which would
	// otherwise shuffle the [macros] block on every save.
	for _, name := range order["macros"] {
		if _, redefined := profile.Macros[name]; !redefined {
			profile.Order = append(profile.Order, name)
		}
		profile.Macros[name] = strings.TrimSpace(sections["macros"][name])
	}

	for _, rawLabel := range order["keys"] {
		value := strings.TrimSpace(sections["keys"][rawLabel])
		if rawLabel == "pad" {
			parts := strings.Fields(value)
			if len(parts) != 4 {
				return nil, fmt.Errorf("%s: pad takes 4 values (up down left right), got %d", path, len(parts))
			}
			for i, label := range []string{"pad_up", "pad_down", "pad_left", "pad_right"} {
				profile.Bindings[label] = parts[i]
			}
			continue
		}
		label, err := canonicalLabel(rawLabel)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		profile.Bindings[label] = value
	}
	return profile, nil
}

// parseINI reads the small INI dialect profiles use: [sections], key = value,
// and # or ; starting a comment. It also returns declaration order per section.
func parseINI(path string) (map[string]map[string]string, map[string][]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()

	sections := map[string]map[string]string{}
	order := map[string][]string{}
	section := ""
	scanner := bufio.NewScanner(file)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if cut := strings.IndexAny(text, "#;"); cut >= 0 {
			text = strings.TrimSpace(text[:cut])
		}
		if text == "" {
			continue
		}
		if strings.HasPrefix(text, "[") && strings.HasSuffix(text, "]") {
			section = strings.ToLower(strings.TrimSpace(text[1 : len(text)-1]))
			if sections[section] == nil {
				sections[section] = map[string]string{}
			}
			continue
		}
		name, value, found := strings.Cut(text, "=")
		if !found {
			return nil, nil, fmt.Errorf("%s:%d: expected `name = value`, got %q", path, line, text)
		}
		if section == "" {
			return nil, nil, fmt.Errorf("%s:%d: %q sits outside any [section]", path, line, name)
		}
		key := strings.ToLower(strings.TrimSpace(name))
		if _, dup := sections[section][key]; dup {
			return nil, nil, fmt.Errorf("%s:%d: %q is set twice in [%s]", path, line, key, section)
		}
		sections[section][key] = strings.TrimSpace(value)
		order[section] = append(order[section], key)
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, err
	}
	return sections, order, nil
}

// Resolve expands an @macro reference; anything else is returned unchanged.
func (p *Profile) Resolve(label, value string) (string, error) {
	if !strings.HasPrefix(value, "@") {
		return value, nil
	}
	name := strings.ToLower(value[1:])
	body, ok := p.Macros[name]
	if !ok {
		return "", fmt.Errorf("%s: no macro named %q", label, name)
	}
	return "macro(" + body + ")", nil
}

// Save writes the profile back out. Only bindings that differ from the parent
// are written, so `extends` keeps doing its job across edits.
func (p *Profile) Save(path string, parent *Profile) error {
	var b strings.Builder
	b.WriteString("[profile]\n")
	fmt.Fprintf(&b, "name = %s\n", p.Name)
	if p.Extends != "" {
		fmt.Fprintf(&b, "extends = %s\n", p.Extends)
	}
	if p.Device != defaultDevice {
		fmt.Fprintf(&b, "device = %s\n", p.Device)
	}

	if len(p.Macros) > 0 {
		b.WriteString("\n[macros]\n")
		for _, name := range p.Order {
			if body, ok := p.Macros[name]; ok {
				fmt.Fprintf(&b, "%s = %s\n", name, body)
			}
		}
	}

	b.WriteString("\n[keys]\n")
	for _, in := range layout {
		value, bound := p.Bindings[in.Label]
		if !bound {
			continue
		}
		if parent != nil {
			if inherited, ok := parent.Bindings[in.Label]; ok && inherited == value {
				continue // the parent already says this
			}
			if _, ok := parent.Bindings[in.Label]; !ok && isOff(value) {
				continue // unbound is the default; no need to say so
			}
		} else if isOff(value) {
			continue
		}
		if value == "" {
			value = "off"
		}
		fmt.Fprintf(&b, "%s = %s\n", in.Label, value)
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}
