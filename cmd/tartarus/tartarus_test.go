package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// TestMain points HOME and XDG_CONFIG_HOME at a throwaway directory for the
// whole test binary. An earlier version of these tests created a profile in the
// developer's real ~/.config/tartarus/profiles, which is nobody's idea of a
// test fixture. Individual tests still override these as needed.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "tartarus-test-home-*")
	if err != nil {
		panic(err)
	}
	os.Setenv("HOME", home)
	os.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	os.Unsetenv("TARTARUS_PROFILES")
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
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

func TestKeydUsesABareDeviceIDByDefault(t *testing.T) {
	// A bare vendor:product is accepted by every keyd version. The `k:` prefix
	// is not in keyd's changelog, so it is not assumed.
	p, err := LoadProfile("diablo4", profileDir(t))
	if err != nil {
		t.Fatal(err)
	}
	out, err := RenderKeyd(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "[ids]\n1532:022b\n") {
		t.Errorf("expected a bare device id:\n%s", out)
	}

	// A profile may narrow it, and whatever it says is passed through.
	for _, id := range []string{"k:1532:022b", "1532:022b:fab63040", "*"} {
		p.Device = id
		out, err := RenderKeyd(p)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "[ids]\n"+id+"\n") {
			t.Errorf("device %q was not passed through:\n%s", id, out)
		}
	}
}

// --- importing old .map files ---

// parseMap reads an hwdb .map into scancode -> value.
func parseMap(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "KEYBOARD_KEY_") {
			continue
		}
		prop, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		out[strings.ToLower(strings.TrimPrefix(prop, "KEYBOARD_KEY_"))] = strings.TrimSpace(value)
	}
	return out
}

func TestImportIsFaithfulToTheMapFiles(t *testing.T) {
	root := repoRoot(t)
	maps, err := filepath.Glob(filepath.Join(root, "config", "tartarus", "*.map"))
	if err != nil || len(maps) == 0 {
		t.Skip("no .map files to import")
	}

	for _, mapPath := range maps {
		slug := strings.TrimSuffix(filepath.Base(mapPath), ".map")
		t.Run(slug, func(t *testing.T) {
			text, _, err := ImportMap(mapPath)
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, slug+profileSuffix), []byte(text), 0o644); err != nil {
				t.Fatal(err)
			}
			p, err := LoadProfile(slug, []string{dir})
			if err != nil {
				t.Fatalf("imported profile does not load: %v", err)
			}
			out, err := RenderKeyd(p)
			if err != nil {
				t.Fatal(err)
			}

			// stock key name -> what the generated config binds it to
			bound := map[string]string{}
			for _, line := range strings.Split(out, "\n") {
				line = strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
				if line == "" || strings.HasPrefix(line, "[") {
					continue
				}
				if k, v, found := strings.Cut(line, "="); found {
					bound[strings.TrimSpace(k)] = strings.TrimSpace(v)
				}
			}

			original := parseMap(t, mapPath)
			repaired := 0
			for _, in := range layout {
				want := original[in.Scancode]
				got := bound[in.Stock]
				// The one intended difference: a key written `0` or left empty
				// meant "unbound" but sent the digit zero. It is now noop.
				if want == "0" || want == "" {
					if got != "noop" {
						t.Errorf("%s: expected the broken unbind to become noop, got %q", in.Label, got)
					}
					repaired++
					continue
				}
				// keyd spells a few keys differently from the kernel, so the
				// comparison is against keyd's name for the .map's value.
				if keydName(want) != got {
					t.Errorf("%s (%s): .map says %q (keyd: %q), generated config says %q",
						in.Label, in.Scancode, want, keydName(want), got)
				}
			}
			if repaired == 0 {
				t.Logf("%s had no broken unbinds", slug)
			} else {
				t.Logf("%s: %d broken unbinds repaired", slug, repaired)
			}
		})
	}
}

func TestImportReadsTheDeviceFromTheMatchLine(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"evdev:input:b0003v1532p022B*", "1532:022b"},
		{"evdev:input:b0003v046DpC52B*", "046d:c52b"},
		{"nonsense", ""},
	} {
		if got := deviceFromModalias(tc.in); got != tc.want {
			t.Errorf("deviceFromModalias(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestImportRejectsAFileWithNoBindings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.map")
	if err := os.WriteFile(path, []byte("# just a comment\nevdev:input:b0003v1532p022B*\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ImportMap(path); err == nil {
		t.Error("expected an error for a .map with no KEYBOARD_KEY_ lines")
	}
}

func TestSummariseKeepsLongErrorListsReadable(t *testing.T) {
	// keyd reported 25 near-identical parse errors for one bad config; dumping
	// them all helps nobody.
	var many []string
	for i := 9; i <= 33; i++ {
		many = append(many, "ERROR: line "+itoa(i)+": invalid key or action")
	}
	got := summarise(many, 8)
	if strings.Count(got, "\n") != 8 {
		t.Errorf("expected 8 lines plus a tail, got:\n%s", got)
	}
	if !strings.Contains(got, "and 17 more") {
		t.Errorf("expected a count of what was elided, got:\n%s", got)
	}
	// A short list is shown in full, with no tail.
	short := summarise([]string{"ERROR: a", "ERROR: b"}, 8)
	if strings.Contains(short, "more") {
		t.Errorf("a short list should not be elided: %q", short)
	}
	for _, tc := range []struct {
		in   int
		want string
	}{{0, "0"}, {7, "7"}, {17, "17"}, {103, "103"}} {
		if got := itoa(tc.in); got != tc.want {
			t.Errorf("itoa(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// --- verifying against the hardware ---

func TestExpectedCodesFromProfile(t *testing.T) {
	p, err := LoadProfile("phasmophobia", profileDir(t))
	if err != nil {
		t.Fatal(err)
	}
	want, err := expectedCodes(p, false)
	if err != nil {
		t.Fatal(err)
	}
	for label, code := range map[string]int{
		"k01": 1,  // esc, inherited from default
		"k03": 57, // space
		"k07": 2,  // digit 1
		"k10": 36, // j
		"k11": 42, // leftshift
		"bar": 20, // t
	} {
		if want[label] != code {
			t.Errorf("%s: expected code %d, got %d", label, code, want[label])
		}
	}
	// The keys the old .map bound to the digit zero must now expect silence.
	for _, label := range []string{"k02", "k04", "k17", "k18", "k19"} {
		if want[label] != 0 {
			t.Errorf("%s should expect silence, got code %d (%s)",
				label, want[label], codeName(want[label]))
		}
	}
	if codeName(0) != silentName {
		t.Errorf("code 0 should read as %q", silentName)
	}
}

func TestExpectedCodesForStockLayout(t *testing.T) {
	want, err := expectedCodes(nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(want) != len(layout) {
		t.Fatalf("expected %d inputs, got %d", len(layout), len(want))
	}
	for _, in := range layout {
		if want[in.Label] != keyboardKeyCodes[in.Stock] {
			t.Errorf("%s: stock is %s (%d), got %d",
				in.Label, in.Stock, keyboardKeyCodes[in.Stock], want[in.Label])
		}
	}
}

func TestExpectedCodesMarksMacrosUnverifiable(t *testing.T) {
	p, err := LoadProfile("macros-example", profileDir(t))
	if err != nil {
		t.Fatal(err)
	}
	want, err := expectedCodes(p, false)
	if err != nil {
		t.Fatal(err)
	}
	// A macro is more than one keycode, so a single press cannot confirm it.
	if want["k03"] != -1 {
		t.Errorf("a macro binding should be marked unverifiable, got %d", want["k03"])
	}
	if codeName(-1) != "macro" {
		t.Errorf("code -1 should read as macro, got %q", codeName(-1))
	}
}

func TestEveryInputHasAVerifyPrompt(t *testing.T) {
	for _, in := range layout {
		if verifyPrompt[in.Label] == "" {
			t.Errorf("%s has no prompt saying where it is", in.Label)
		}
	}
}

func TestReaderNeverBlocksPastItsTimeout(t *testing.T) {
	// verify hung on the first key because draining the terminal blocked. These
	// guard the two places that could wait forever.
	r := &eventReader{events: make(chan uint16, 4), ttyFD: -1}

	start := time.Now()
	if _, ok := r.Next(50 * time.Millisecond); ok {
		t.Error("expected no key from an empty stream")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Next waited %s past a 50ms timeout", elapsed)
	}

	start = time.Now()
	r.Drain()
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Drain took %s with nothing to drain", elapsed)
	}

	// A queued press is returned, and the queue is left empty afterwards.
	r.events <- 30
	if code, ok := r.Next(time.Second); !ok || code != 30 {
		t.Errorf("expected keycode 30, got %d (ok=%v)", code, ok)
	}
	r.events <- 31
	r.Drain()
	if _, ok := r.Next(50 * time.Millisecond); ok {
		t.Error("Drain left a press queued")
	}
}

func TestInstalledBinaryDoesNotLookBesideItself(t *testing.T) {
	// An installed binary reported /usr/bin/profiles in its search path, which
	// can never exist and only clutters `tartarus version`.
	for _, dir := range []string{"/usr/bin", "/usr/local/bin", "/bin", "/usr/sbin"} {
		if !isBinDir(dir) {
			t.Errorf("%s should be recognised as a bin directory", dir)
		}
	}
	for _, dir := range []string{"/home/someone/src/tartarus", "/opt/tartarus", "."} {
		if isBinDir(dir) {
			t.Errorf("%s is not a bin directory", dir)
		}
	}
	for _, dir := range ProfileDirs("") {
		if isBinDir(filepath.Dir(dir)) && filepath.Base(dir) == "profiles" {
			t.Errorf("search path still includes %s", dir)
		}
	}
}
