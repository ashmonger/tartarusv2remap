package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

const usage = `tartarus — remap a Razer Tartarus V2 keypad per game, through keyd

usage: tartarus [flags] <command> [profile]

  new SLUG          create a profile and open the editor
  edit [PROFILE]    open the keypad editor
  list              list profiles, marking the active one
  show PROFILE      print the keypad with its bindings
  compile PROFILE   print the keyd config (no privileges needed)
  apply PROFILE     install and activate
  off               remove the mapping, restore the stock layout
  status            print the active profile
  check             validate every profile
  keys [PATTERN]    list the keyboard keys a profile may use
  import FILE.map   convert an old udev .map file into a profile
  version           print the build, the keyd it found, and the profiles in scope
  verify [PROFILE]  press each key and check it against the profile (needs root)
  devices           show which input devices verify can see and would read

flags:
  --profiles DIR    directory holding *.profile files
  --dry-run         show what apply would write and run, without doing it
  --from PROFILE    for new: start from a copy of this profile
  --name NAME       for new: the readable name (defaults to the slug)
  --no-edit         for new: just write the file, do not open the editor
  --stock           for verify: compare against the keypad's stock layout
  --timeout SECS    for verify: seconds to wait per key (default 8)
  --device PATH     for verify: read this event device instead of guessing

examples:
  tartarus new eldenring                 a fresh profile, extending default
  tartarus new bg3 --from diablo4        start from an existing profile
  tartarus new hd2b --name "HD2 (bots)"  set the readable name
`

// Flags that only `new` consumes, plus whether the profile directory was named
// explicitly, which decides if `new` may write to a system path.
var (
	newFrom          string
	newName          string
	newNoEdit        bool
	profilesExplicit bool
	verifyStock      bool
	verifyTimeout    float64
	verifyDevice     string
)

func main() {
	var profilesDir string
	var dryRun bool

	// Go's flag package stops at the first non-flag argument, so the loop below
	// parses in passes against the same bound variables.
	fs := flag.NewFlagSet("tartarus", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	fs.StringVar(&profilesDir, "profiles", "", "directory holding *.profile files")
	fs.BoolVar(&dryRun, "dry-run", false, "show what would change")
	fs.StringVar(&newFrom, "from", "", "for new: start from a copy of this profile")
	fs.StringVar(&newName, "name", "", "for new: the readable name")
	fs.BoolVar(&newNoEdit, "no-edit", false, "for new: do not open the editor")
	var showVersion bool
	fs.BoolVar(&showVersion, "version", false, "print the build and exit")
	fs.BoolVar(&verifyStock, "stock", false, "for verify: compare against the stock layout")
	fs.Float64Var(&verifyTimeout, "timeout", 8, "for verify: seconds to wait per key")
	fs.StringVar(&verifyDevice, "device", "", "for verify: read this event device")

	// Parse repeatedly, peeling off one positional at a time, so a flag is
	// honoured wherever it appears: `new bg3 --from diablo4` reads the same as
	// `--from diablo4 new bg3`.
	var args []string
	remaining := os.Args[1:]
	for len(remaining) > 0 {
		if err := fs.Parse(remaining); err != nil {
			// Asking for help is not an error.
			if errors.Is(err, flag.ErrHelp) {
				os.Exit(0)
			}
			os.Exit(2)
		}
		rest := fs.Args()
		if len(rest) == 0 {
			break
		}
		args = append(args, rest[0])
		remaining = rest[1:]
	}
	profilesExplicit = profilesDir != ""
	if showVersion {
		printVersion(ProfileDirs(profilesDir))
		os.Exit(0)
	}
	if len(args) == 0 {
		fs.Usage()
		os.Exit(2)
	}
	dirs := ProfileDirs(profilesDir)

	if err := run(args, dirs, dryRun); err != nil {
		fmt.Fprintln(os.Stderr, "error: "+err.Error())
		os.Exit(1)
	}
}

func run(args []string, dirs []string, dryRun bool) error {
	command := args[0]
	arg := ""
	if len(args) > 1 {
		arg = args[1]
	}

	switch command {
	case "new":
		return cmdNew(arg, dirs, dryRun)
	case "edit":
		return cmdEdit(arg, dirs, dryRun)
	case "list":
		return cmdList(dirs)
	case "show":
		return cmdShow(arg, dirs)
	case "compile":
		return cmdCompile(arg, dirs)
	case "apply":
		return cmdApply(arg, dirs, dryRun)
	case "off":
		// A dry run only reports; it must never cost a password prompt.
		if !dryRun {
			if err := Elevate(offArgs(dryRun)); err != nil {
				return err
			}
		}
		return Off(dryRun)
	case "status":
		if active := ActiveProfile(); active == "" {
			fmt.Println("no mapping installed (stock layout)")
		} else {
			fmt.Printf("active profile: %s  (%s)\n", active, keydConf)
		}
		return nil
	case "check":
		return cmdCheck(dirs)
	case "keys":
		return cmdKeys(arg)
	case "import":
		return cmdImport(arg)
	case "version":
		printVersion(dirs)
		return nil
	case "devices":
		return cmdDevices()
	case "verify":
		return cmdVerify(arg, dirs, verifyStock, time.Duration(verifyTimeout*1000)*time.Millisecond)
	default:
		return fmt.Errorf("unknown command %q", command)
	}
}

func offArgs(dryRun bool) []string {
	args := []string{"off"}
	if dryRun {
		args = append(args, "--dry-run")
	}
	return args
}

func firstProfile(arg string, dirs []string) (string, error) {
	if arg != "" {
		return arg, nil
	}
	if active := ActiveProfile(); active != "" && active != "unknown" {
		return active, nil
	}
	slugs := ListProfiles(dirs)
	if len(slugs) == 0 {
		return "", fmt.Errorf("no profiles found in: %s", strings.Join(dirs, ", "))
	}
	return slugs[0], nil
}

func cmdNew(slug string, dirs []string, dryRun bool) error {
	path, err := CreateProfile(slug, newFrom, newName, dirs)
	if err != nil {
		return err
	}
	fmt.Printf("created %s\n", path)
	if newNoEdit {
		fmt.Printf("bind keys with: tartarus edit %s\n", slug)
		return nil
	}
	if err := cmdEdit(slug, dirs, dryRun); err != nil {
		// The profile exists either way; say so rather than looking like a failure.
		return fmt.Errorf("%w\n       the profile was created; edit it with: tartarus edit %s", err, slug)
	}
	return nil
}

func cmdEdit(arg string, dirs []string, dryRun bool) error {
	if arg != "" {
		if _, err := findProfile(arg, dirs); err != nil {
			return fmt.Errorf("%w\n       to start one: tartarus new %s", err, arg)
		}
	}
	slug, err := firstProfile(arg, dirs)
	if err != nil {
		return err
	}
	applySlug, err := RunEditor(slug, dirs)
	if err != nil {
		return err
	}
	if applySlug == "" {
		return nil
	}
	return cmdApply(applySlug, dirs, dryRun)
}

func cmdList(dirs []string) error {
	slugs := ListProfiles(dirs)
	if len(slugs) == 0 {
		return fmt.Errorf("no profiles found in: %s", strings.Join(dirs, ", "))
	}
	active := ActiveProfile()
	for _, slug := range slugs {
		profile, err := LoadProfile(slug, dirs)
		if err != nil {
			fmt.Printf("  %-16s (broken: %v)\n", slug, err)
			continue
		}
		mark := " "
		if slug == active {
			mark = "*"
		}
		fmt.Printf("%s %-16s %s\n", mark, slug, profile.Name)
	}
	return nil
}

func cmdShow(arg string, dirs []string) error {
	slug, err := firstProfile(arg, dirs)
	if err != nil {
		return err
	}
	profile, err := LoadProfile(slug, dirs)
	if err != nil {
		return err
	}
	fmt.Printf("Razer Tartarus V2 — %s  [%s]\n\n", profile.Name, profile.Slug)
	for _, row := range gridRows {
		var cells []string
		for _, label := range row {
			cells = append(cells, fmt.Sprintf("%-8s", face(profile, label)))
		}
		fmt.Println("  " + strings.Join(cells, "") + "  " + strings.Join(row, " "))
	}
	fmt.Println()
	for _, label := range thumbCluster {
		fmt.Printf("  %-10s %s\n", label, face(profile, label))
	}
	return nil
}

func face(p *Profile, label string) string {
	value, bound := p.Bindings[label]
	if !bound || isOff(value) {
		return "·"
	}
	return value
}

func cmdCompile(arg string, dirs []string) error {
	if arg == "" {
		return fmt.Errorf("compile needs a profile name")
	}
	profile, err := LoadProfile(arg, dirs)
	if err != nil {
		return err
	}
	content, err := RenderKeyd(profile)
	if err != nil {
		return err
	}
	fmt.Print(content)
	return nil
}

func cmdApply(arg string, dirs []string, dryRun bool) error {
	slug, err := firstProfile(arg, dirs)
	if err != nil {
		return err
	}
	profile, err := LoadProfile(slug, dirs)
	if err != nil {
		return err
	}
	// Compile before asking for a password, so mistakes cost nothing.
	if _, err := RenderKeyd(profile); err != nil {
		return err
	}
	if !dryRun {
		elevated := []string{"apply", slug}
		if dir := os.Getenv("TARTARUS_PROFILES"); dir != "" {
			elevated = append([]string{"--profiles", dir}, elevated...)
		}
		if err := Elevate(elevated); err != nil {
			return err
		}
	}
	if err := Apply(profile, dryRun); err != nil {
		return err
	}
	if !dryRun {
		fmt.Printf("active profile: %s (%s)\n", slug, profile.Name)
	}
	return nil
}

func cmdCheck(dirs []string) error {
	slugs := ListProfiles(dirs)
	failures := 0
	for _, slug := range slugs {
		profile, err := LoadProfile(slug, dirs)
		if err == nil {
			_, err = RenderKeyd(profile)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "FAIL %s: %v\n", slug, err)
			failures++
			continue
		}
		unknown := unknownValues(profile)
		status := "ok  "
		if len(unknown) > 0 {
			status = "WARN"
		}
		fmt.Printf("%s %-16s %d bindings\n", status, slug, len(profile.Bindings))
		for _, u := range unknown {
			fmt.Fprintf(os.Stderr, "       %s  (not a keyboard key; keyd check will judge it)\n", u)
		}
	}
	if failures > 0 {
		return fmt.Errorf("%d profile(s) failed", failures)
	}
	return nil
}

// unknownValues reports bindings that are neither a keyboard key, an unbind,
// nor something shaped like a keyd action.
func unknownValues(p *Profile) []string {
	var out []string
	for _, in := range layout {
		value, bound := p.Bindings[in.Label]
		if !bound || isOff(value) {
			continue
		}
		resolved, err := p.Resolve(in.Label, value)
		if err != nil {
			out = append(out, in.Label+" = "+value)
			continue
		}
		if isPlainKey(resolved) || strings.ContainsAny(resolved, "(+-") {
			continue
		}
		out = append(out, in.Label+" = "+value)
	}
	sort.Strings(out)
	return out
}

func cmdImport(path string) error {
	if path == "" {
		return fmt.Errorf("import needs a path to a .map file")
	}
	profile, notes, err := ImportMap(path)
	if err != nil {
		return err
	}
	for _, note := range notes {
		fmt.Fprintln(os.Stderr, "note: "+note)
	}
	fmt.Print(profile)
	return nil
}

func cmdKeys(pattern string) error {
	needle := strings.ToLower(pattern)
	shown := 0
	for _, name := range sortedKeyNames {
		if needle != "" && !strings.Contains(name, needle) {
			continue
		}
		fmt.Printf("%-20s %d\n", name, keyboardKeyCodes[name])
		shown++
	}
	if shown == 0 {
		return fmt.Errorf("no keyboard key matches %q", pattern)
	}
	fmt.Fprintf(os.Stderr, "\n%d of %d keyboard keys\n", shown, len(keyboardKeyCodes))
	return nil
}
