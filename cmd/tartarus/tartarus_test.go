package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot walks up from the test's working directory to the module root, so
// the tests do not care how deep the package sits.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find the module root (no go.mod above the test)")
		}
		dir = parent
	}
}

func profileDir(t *testing.T) []string {
	t.Helper()
	return []string{filepath.Join(repoRoot(t), "profiles")}
}

func TestLayoutInvariants(t *testing.T) {
	if len(layout) != 25 {
		t.Fatalf("expected 25 bindable inputs, got %d", len(layout))
	}
	seenLabel := map[string]bool{}
	seenStock := map[string]bool{}
	seenCode := map[string]bool{}
	for _, in := range layout {
		if seenLabel[in.Label] {
			t.Errorf("duplicate label %q", in.Label)
		}
		// keyd matches on the stock name, so a duplicate would silently collide.
		if seenStock[in.Stock] {
			t.Errorf("duplicate stock key %q", in.Stock)
		}
		if seenCode[in.Scancode] {
			t.Errorf("duplicate scancode %q", in.Scancode)
		}
		if !isPlainKey(in.Stock) {
			t.Errorf("stock key %q for %s is not a keyboard key", in.Stock, in.Label)
		}
		seenLabel[in.Label], seenStock[in.Stock], seenCode[in.Scancode] = true, true, true
	}
}

func TestKeyTable(t *testing.T) {
	if len(keyboardKeyCodes) != 138 {
		t.Fatalf("expected 138 keyboard keys, got %d", len(keyboardKeyCodes))
	}
	// The whole point of the redesign: `0` is the digit zero, not an unbind.
	if code := keyboardKeyCodes["0"]; code != 11 {
		t.Errorf("expected key 0 to be code 11, got %d", code)
	}
	if isOff("0") {
		t.Error("`0` must never be treated as an unbind")
	}
	for _, word := range []string{"off", "none", "-", "", "noop"} {
		if !isOff(word) {
			t.Errorf("%q should mean unbound", word)
		}
	}
	if keyboardKeyCodes["zenkakuhankaku"] != 85 {
		t.Errorf("key table is off by one past the hole at code 84")
	}
}

func TestRenderKeydBindsEveryInput(t *testing.T) {
	p, err := LoadProfile("diablo4", profileDir(t))
	if err != nil {
		t.Fatal(err)
	}
	out, err := RenderKeyd(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(out, " = "); got != 25 {
		t.Errorf("expected 25 bindings in the config, got %d", got)
	}
	if !strings.Contains(out, "[ids]\n1532:022b") {
		t.Error("config does not claim the keypad")
	}
	if !strings.Contains(out, "noop") {
		t.Error("unbound keys should compile to noop")
	}
	if strings.Contains(out, "= 0 ") {
		t.Error("config must never bind the digit zero by accident")
	}
	if !strings.Contains(out, stampLine+" diablo4") {
		t.Error("config is not stamped with the profile slug")
	}
}

func TestExtendsAndOverride(t *testing.T) {
	dirs := profileDir(t)
	// shapeofdreams turns off two keys that default.profile binds.
	p, err := LoadProfile("shapeofdreams", dirs)
	if err != nil {
		t.Fatal(err)
	}
	if !isOff(p.Bindings["k09"]) || !isOff(p.Bindings["k10"]) {
		t.Error("`off` did not override the inherited binding")
	}
	if p.Bindings["pad_up"] != "w" {
		t.Errorf("inherited pad binding lost: %q", p.Bindings["pad_up"])
	}
	if p.Bindings["k11"] != "leftctrl" {
		t.Errorf("own binding lost: %q", p.Bindings["k11"])
	}

	out, err := RenderKeyd(p)
	if err != nil {
		t.Fatal(err)
	}
	// keyd spells the kernel's leftctrl differently.
	if !strings.Contains(out, "leftcontrol") {
		t.Error("leftctrl was not translated to keyd's spelling")
	}
}

func TestMacrosResolve(t *testing.T) {
	p, err := LoadProfile("macros-example", profileDir(t))
	if err != nil {
		t.Fatal(err)
	}
	out, err := RenderKeyd(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "macro(1 100ms 2 100ms 3)") {
		t.Errorf("named macro did not expand:\n%s", out)
	}
	if !strings.Contains(out, "= C-c") {
		t.Error("chord binding was mangled")
	}
}

func TestUnknownMacroIsAnError(t *testing.T) {
	p := &Profile{
		Slug: "x", Name: "x", Device: defaultDevice,
		Bindings: map[string]string{"k01": "@nope"},
		Macros:   map[string]string{},
	}
	if _, err := RenderKeyd(p); err == nil {
		t.Error("expected an error for a macro that is not defined")
	}
}

func TestInheritanceLoopIsCaught(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name+profileSuffix), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a", "[profile]\nextends = b\n")
	write("b", "[profile]\nextends = a\n")
	if _, err := LoadProfile("a", []string{dir}); err == nil {
		t.Error("expected an inheritance loop to be reported")
	} else if !strings.Contains(err.Error(), "loops") {
		t.Errorf("unhelpful error: %v", err)
	}
}

func TestSaveRoundTrip(t *testing.T) {
	dirs := profileDir(t)
	original, err := LoadProfile("diablo4", dirs)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := LoadProfile("default", dirs)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	for _, slug := range []string{"default", "diablo4"} {
		src, err := os.ReadFile(filepath.Join(repoRoot(t), "profiles", slug+profileSuffix))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, slug+profileSuffix), src, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, "diablo4"+profileSuffix)
	if err := original.Save(path, parent); err != nil {
		t.Fatal(err)
	}

	reloaded, err := LoadProfile("diablo4", []string{dir})
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range layout {
		want, hadWant := original.Bindings[in.Label]
		got, hadGot := reloaded.Bindings[in.Label]
		if isOff(want) && isOff(got) {
			continue
		}
		if hadWant != hadGot || want != got {
			t.Errorf("%s changed across save/reload: %q -> %q", in.Label, want, got)
		}
	}
}

func TestDuplicateOptionIsAnError(t *testing.T) {
	dir := t.TempDir()
	body := "[profile]\nname = dup\n\n[keys]\nk01 = a\nk01 = b\n"
	if err := os.WriteFile(filepath.Join(dir, "dup"+profileSuffix), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadProfile("dup", []string{dir}); err == nil {
		t.Error("expected a duplicate binding to be rejected")
	}
}

func TestMacroOrderIsStable(t *testing.T) {
	dirs := profileDir(t)
	// Go randomises map iteration, so a profile loaded repeatedly must still
	// report its macros in declaration order.
	var first []string
	for i := 0; i < 20; i++ {
		p, err := LoadProfile("macros-example", dirs)
		if err != nil {
			t.Fatal(err)
		}
		if first == nil {
			first = p.Order
			continue
		}
		if len(p.Order) != len(first) {
			t.Fatalf("macro count changed between loads: %v vs %v", first, p.Order)
		}
		for j := range first {
			if p.Order[j] != first[j] {
				t.Fatalf("macro order changed between loads: %v vs %v", first, p.Order)
			}
		}
	}
	if want := []string{"greet", "count", "save_all"}; len(first) != len(want) {
		t.Errorf("expected %v, got %v", want, first)
	} else {
		for i := range want {
			if first[i] != want[i] {
				t.Errorf("expected declaration order %v, got %v", want, first)
				break
			}
		}
	}
}

func TestSaveIsByteStable(t *testing.T) {
	dirs := profileDir(t)
	p, err := LoadProfile("macros-example", dirs)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := LoadProfile("default", dirs)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "out"+profileSuffix)
	var reference string
	for i := 0; i < 20; i++ {
		if err := p.Save(path, parent); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			reference = string(data)
			continue
		}
		if string(data) != reference {
			t.Fatalf("save output is not stable:\n--- first ---\n%s\n--- now ---\n%s", reference, data)
		}
	}
}

// --- creating profiles ---

func tempProfiles(t *testing.T, slugs ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, slug := range slugs {
		src, err := os.ReadFile(filepath.Join(repoRoot(t), "profiles", slug+profileSuffix))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, slug+profileSuffix), src, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestCreateFromScratch(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := tempProfiles(t, "default")
	t.Setenv("TARTARUS_PROFILES", dir)
	path, err := CreateProfile("eldenring", "", "", []string{dir})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "eldenring.profile"); path != want {
		t.Errorf("wrote to %s, wanted %s", path, want)
	}

	p, err := LoadProfile("eldenring", []string{dir})
	if err != nil {
		t.Fatalf("a new profile must load immediately: %v", err)
	}
	if p.Extends != "default" {
		t.Errorf("expected the new profile to extend default, got %q", p.Extends)
	}
	// It inherits default's bindings and nothing else.
	if p.Bindings["pad_up"] != "w" || p.Bindings["k01"] != "esc" {
		t.Errorf("inherited bindings missing: %v", p.Bindings)
	}
	out, err := RenderKeyd(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(out, " = "); got != 25 {
		t.Errorf("a new profile should still bind all 25 inputs, got %d", got)
	}
	// The skeleton must name the real slug, not one guessed from the name.
	body, _ := os.ReadFile(path)
	if !strings.Contains(string(body), "tartarus edit eldenring") {
		t.Errorf("skeleton does not reference its own slug:\n%s", body)
	}
}

func TestCreateStandaloneWhenNoDefault(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()
	t.Setenv("TARTARUS_PROFILES", dir)
	if _, err := CreateProfile("solo", "", "", []string{dir}); err != nil {
		t.Fatal(err)
	}
	p, err := LoadProfile("solo", []string{dir})
	if err != nil {
		t.Fatal(err)
	}
	if p.Extends != "" {
		t.Errorf("with no default.profile present, extends should be empty, got %q", p.Extends)
	}
}

func TestCreateFromExistingProfile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := tempProfiles(t, "default", "diablo4")
	t.Setenv("TARTARUS_PROFILES", dir)
	if _, err := CreateProfile("bg3", "diablo4", "Baldur's Gate 3", []string{dir}); err != nil {
		t.Fatal(err)
	}
	clone, err := LoadProfile("bg3", []string{dir})
	if err != nil {
		t.Fatal(err)
	}
	source, err := LoadProfile("diablo4", []string{dir})
	if err != nil {
		t.Fatal(err)
	}
	if clone.Name != "Baldur's Gate 3" {
		t.Errorf("clone kept the wrong name: %q", clone.Name)
	}
	for _, in := range layout {
		if clone.Bindings[in.Label] != source.Bindings[in.Label] {
			t.Errorf("%s differs from the source: %q vs %q",
				in.Label, clone.Bindings[in.Label], source.Bindings[in.Label])
		}
	}
}

func TestCreateRefusesDuplicate(t *testing.T) {
	dir := tempProfiles(t, "default", "diablo4")
	if _, err := CreateProfile("diablo4", "", "", []string{dir}); err == nil {
		t.Error("expected an existing profile to be protected")
	} else if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("unhelpful error: %v", err)
	}
}

func TestCreateRefusesBadSlugs(t *testing.T) {
	dir := t.TempDir()
	for _, slug := range []string{"", "../escape", "with/slash", "UPPER", "sp ace", ".hidden",
		strings.Repeat("x", 65)} {
		if _, err := CreateProfile(slug, "", "", []string{dir}); err == nil {
			t.Errorf("slug %q should have been refused", slug)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("a refused slug left files behind: %v", entries)
	}
}

func TestCreateRefusesMissingSourceWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	if _, err := CreateProfile("x", "nosuch", "", []string{dir}); err == nil {
		t.Error("expected a missing --from profile to be an error")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("a failed create left files behind: %v", entries)
	}
	if _, err := CreateProfile("selfref", "selfref", "", []string{dir}); err == nil {
		t.Error("expected copying a profile onto itself to be an error")
	}
}

func TestRetitleInsertsNameWhenAbsent(t *testing.T) {
	got := retitle("[profile]\nextends = default\n\n[keys]\nk01 = esc\n", "New Name")
	if !strings.Contains(got, "name = New Name") {
		t.Errorf("name was not inserted:\n%s", got)
	}
	if !strings.Contains(got, "extends = default") || !strings.Contains(got, "k01 = esc") {
		t.Errorf("retitle lost content:\n%s", got)
	}
	// A name outside [profile] must not be mistaken for the profile's own.
	got = retitle("[keys]\nname = a\n", "Real")
	if strings.Contains(got, "[keys]\nname = Real") {
		t.Errorf("retitle rewrote a binding called name:\n%s", got)
	}
}

// --- installed layout ---

func TestProfileDirsIncludeSystemPaths(t *testing.T) {
	dirs := ProfileDirs("")
	var sawAdmin, sawSystem bool
	for _, dir := range dirs {
		sawAdmin = sawAdmin || dir == AdminProfileDir
		sawSystem = sawSystem || dir == SystemProfileDir
	}
	if !sawAdmin || !sawSystem {
		t.Errorf("an installed binary must look in %s and %s; got %v",
			AdminProfileDir, SystemProfileDir, dirs)
	}
	// The packaged defaults must come last, so a user's own copy shadows them.
	if dirs[len(dirs)-2] != SystemProfileDir && dirs[len(dirs)-1] != SystemProfileDir {
		t.Errorf("packaged profiles should be searched last, got %v", dirs)
	}
	if override := ProfileDirs("/tmp/x"); len(override) != 1 || override[0] != "/tmp/x" {
		t.Errorf("--profiles should win outright, got %v", override)
	}
}

func TestNewWritesToTheUserConfigByDefault(t *testing.T) {
	// The regression this guards: `new` used to return the first existing
	// writable directory in the search path, so a relative `profiles` directory
	// in the current working directory captured the new profile.
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv("TARTARUS_PROFILES", "")

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	if err := os.MkdirAll("profiles", 0o755); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(cwd) }()

	path, err := CreateProfile("citest", "", "", ProfileDirs(""))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(config, "tartarus", "profiles")
	if filepath.Dir(path) != want {
		t.Errorf("wrote to %s, expected it under %s", path, want)
	}
	if _, err := os.Stat(filepath.Join("profiles", "citest.profile")); err == nil {
		t.Error("a relative profiles directory must not capture the new profile")
	}
}

func TestNewHonoursTartarusProfilesEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("TARTARUS_PROFILES", dir)

	path, err := CreateProfile("envgame", "", "", ProfileDirs(""))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != dir {
		t.Errorf("wrote to %s, expected it under %s", path, dir)
	}
}

func TestPackagedProfilesAllLoad(t *testing.T) {
	// Every profile the .deb ships must load and compile; a broken one would
	// ship broken.
	dirs := profileDir(t)
	slugs := ListProfiles(dirs)
	if len(slugs) < 6 {
		t.Fatalf("expected the shipped profiles, found %v", slugs)
	}
	for _, slug := range slugs {
		p, err := LoadProfile(slug, dirs)
		if err != nil {
			t.Errorf("%s does not load: %v", slug, err)
			continue
		}
		out, err := RenderKeyd(p)
		if err != nil {
			t.Errorf("%s does not compile: %v", slug, err)
			continue
		}
		if got := strings.Count(out, " = "); got != 25 {
			t.Errorf("%s binds %d inputs, expected 25", slug, got)
		}
	}
}

func TestExplicitProfilesDirIsHonoured(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("TARTARUS_PROFILES", "")

	// --profiles names the directory outright, even a system-looking one.
	profilesExplicit = true
	defer func() { profilesExplicit = false }()

	dir := filepath.Join(t.TempDir(), "usr", "share", "tartarus", "profiles")
	path, err := CreateProfile("b", "", "", []string{dir})
	if err != nil {
		t.Fatalf("an explicitly named directory should be used: %v", err)
	}
	if filepath.Dir(path) != dir {
		t.Errorf("wrote to %s, expected %s", path, dir)
	}
}

func TestNewResolvesExtendsFromAnotherDirectory(t *testing.T) {
	// The installed shape: default.profile is read-only under /usr/share while
	// the new profile is written to the user's config. Validating only where
	// the file landed would fail to find its parent and delete it.
	packaged := tempProfiles(t, "default")
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv("TARTARUS_PROFILES", "")

	dirs := []string{filepath.Join(config, "tartarus", "profiles"), packaged}
	path, err := CreateProfile("eldenring", "", "", dirs)
	if err != nil {
		t.Fatalf("a new profile must resolve a parent from elsewhere on the path: %v", err)
	}
	if filepath.Dir(path) == packaged {
		t.Errorf("wrote into the packaged directory: %s", path)
	}

	p, err := LoadProfile("eldenring", dirs)
	if err != nil {
		t.Fatal(err)
	}
	if p.Extends != "default" {
		t.Errorf("expected the new profile to extend the packaged default, got %q", p.Extends)
	}
	if p.Bindings["k01"] != "esc" {
		t.Errorf("packaged default's bindings were not inherited: %v", p.Bindings)
	}
}
