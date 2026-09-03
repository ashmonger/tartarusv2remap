package main

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Verifying against the hardware lived in a side script for a while, and a
// stale copy of it reported the wrong answer three times. It belongs in the
// binary, where it is installed and updated together with everything else, and
// where it can compare against the profile itself rather than re-parsing the
// config that was generated from it.

const (
	evKey      = 0x01
	eventSize  = 24 // input_event on 64-bit: 16-byte timeval, then type, code, value
	silentName = "silent"
)

type inputDevice struct {
	Path string
	Name string
}

var (
	reName     = regexp.MustCompile(`N: Name="([^"]*)"`)
	reHandlers = regexp.MustCompile(`H: Handlers=(.*)`)
)

// inputDevices lists event devices whose block satisfies match.
func inputDevices(match func(block, name string) bool) []inputDevice {
	data, err := os.ReadFile("/proc/bus/input/devices")
	if err != nil {
		return nil
	}
	var found []inputDevice
	for _, block := range strings.Split(string(data), "\n\n") {
		name := reName.FindStringSubmatch(block)
		handlers := reHandlers.FindStringSubmatch(block)
		if name == nil || handlers == nil || !match(block, name[1]) {
			continue
		}
		for _, token := range strings.Fields(handlers[1]) {
			if strings.HasPrefix(token, "event") {
				found = append(found, inputDevice{"/dev/input/" + token, name[1]})
				break
			}
		}
	}
	return found
}

// keypadDevices are the keypad's own event devices that can report keys.
func keypadDevices(device string) []inputDevice {
	vendor, product, _ := strings.Cut(device, ":")
	return inputDevices(func(block, _ string) bool {
		return strings.Contains(block, "Vendor="+vendor) &&
			strings.Contains(block, "Product="+product) &&
			strings.Contains(block, "B: KEY=")
	})
}

// keydVirtualDevice is what keyd emits through. While keyd is active it holds
// an exclusive grab on the keypad, so this is the only place its output appears.
func keydVirtualDevice() *inputDevice {
	candidates := inputDevices(func(block, name string) bool {
		lowered := strings.ToLower(name)
		return strings.Contains(lowered, "keyd") &&
			!strings.Contains(lowered, "pointer") &&
			!strings.Contains(lowered, "mouse") &&
			strings.Contains(block, "B: KEY=")
	})
	for _, candidate := range candidates {
		if strings.Contains(strings.ToLower(candidate.Name), "virtual keyboard") {
			return &candidate
		}
	}
	if len(candidates) > 0 {
		return &candidates[0]
	}
	return nil
}

// expectedCodes is what each input should send, by keycode. A zero value means
// the key is expected to send nothing.
func expectedCodes(p *Profile, stock bool) (map[string]int, error) {
	want := map[string]int{}
	for _, in := range layout {
		if stock {
			want[in.Label] = keyboardKeyCodes[in.Stock]
			continue
		}
		value, bound := p.Bindings[in.Label]
		if !bound {
			want[in.Label] = 0 // unbound keys send nothing
			continue
		}
		resolved, err := p.Resolve(in.Label, value)
		if err != nil {
			return nil, err
		}
		if isOff(resolved) {
			want[in.Label] = 0
			continue
		}
		code, known := keyboardKeyCodes[strings.ToLower(resolved)]
		if !known {
			want[in.Label] = -1 // a macro; cannot be checked by one keycode
			continue
		}
		want[in.Label] = code
	}
	return want, nil
}

func codeName(code int) string {
	switch code {
	case 0:
		return silentName
	case -1:
		return "macro"
	}
	for name, c := range keyboardKeyCodes {
		if c == code {
			return name
		}
	}
	return fmt.Sprintf("code %d", code)
}

func cmdVerify(arg string, dirs []string, stock bool, perKey time.Duration) error {
	if len(keypadDevices(defaultDevice)) == 0 {
		return fmt.Errorf("no keypad (%s) found; is it plugged in?", defaultDevice)
	}

	var profile *Profile
	want := map[string]int{}
	comparing := "the keypad's stock layout"
	if !stock {
		slug, err := firstProfile(arg, dirs)
		if err != nil {
			return err
		}
		if profile, err = LoadProfile(slug, dirs); err != nil {
			return err
		}
		comparing = fmt.Sprintf("profile %s", slug)
	}
	var err error
	if want, err = expectedCodes(profile, stock); err != nil {
		return err
	}

	// While keyd is active it owns the keypad, so its output is what to read.
	virtual := keydVirtualDevice()
	viaKeyd := !stock && ActiveProfile() != "" && virtual != nil
	if stock && ActiveProfile() != "" {
		return fmt.Errorf(
			"a keyd profile (%s) is active, so the keypad is not at stock. "+
				"Run `tartarus off` first, or drop --stock", ActiveProfile())
	}

	devices := keypadDevices(defaultDevice)
	switch {
	case verifyDevice != "":
		devices = []inputDevice{{Path: verifyDevice, Name: "named with --device"}}
		viaKeyd = true // a named device is read as-is, never grabbed
	case viaKeyd:
		devices = []inputDevice{*virtual}
	case !stock && ActiveProfile() != "":
		return fmt.Errorf(
			"profile %q is active, so keyd holds the keypad and its output comes from\n"+
				"keyd's own device, which was not recognised. Run `tartarus devices` to\n"+
				"see what is there, then pass --device with the right path",
			ActiveProfile())
	}

	if err := Elevate(append([]string{"verify"}, verifyFlags(arg, stock)...)); err != nil {
		return err
	}

	if verifyWatch > 0 {
		return watchKeys(devices, !viaKeyd, verifyWatch)
	}

	fmt.Printf("Razer Tartarus V2 check — comparing against %s\n\n", comparing)
	for _, d := range devices {
		fmt.Printf("  %s  %s\n", d.Path, d.Name)
	}
	if viaKeyd {
		fmt.Println("\nkeyd holds the keypad, so this reads what keyd emits.")
	}
	fmt.Printf("\nPress each key as prompted. Quiet for %s counts as sending nothing.\n\n",
		perKey)

	reader, err := openReader(devices, !viaKeyd)
	if err != nil {
		return err
	}
	defer reader.Close()

	matched, results := 0, make([]string, 0, len(layout))
	for _, in := range layout {
		fmt.Printf("  %-10s %-42s", in.Label, verifyPrompt[in.Label])
		reader.Drain()
		code, ok := reader.Next(perKey)
		got := 0
		if ok {
			got = int(code)
		}
		expect := want[in.Label]
		verdict := "ok"
		switch {
		case expect == -1:
			verdict = "macro, not checked"
		case got != expect:
			verdict = fmt.Sprintf("DIFFERS (expected %s)", codeName(expect))
		default:
			matched++
		}
		fmt.Printf(" -> %-12s %s\n", codeName(got), verdict)
		results = append(results, fmt.Sprintf("  %-10s %s", in.Label, codeName(got)))
	}

	fmt.Printf("\n%d/%d inputs match %s\n", matched, len(layout), comparing)
	if matched != len(layout) {
		fmt.Println("\nwhat each key sent:")
		fmt.Println(strings.Join(results, "\n"))
	}
	if matched == len(layout) {
		return nil
	}
	return fmt.Errorf("%d input(s) did not match", len(layout)-matched)
}

func verifyFlags(arg string, stock bool) []string {
	// Everything that changes what verify does has to survive the sudo re-exec,
	// or the elevated run quietly does something else.
	var flags []string
	if dir := os.Getenv("TARTARUS_PROFILES"); dir != "" {
		flags = append(flags, "--profiles", dir)
	}
	if stock {
		flags = append(flags, "--stock")
	}
	if verifyDevice != "" {
		flags = append(flags, "--device", verifyDevice)
	}
	if verifyTimeout != 8 {
		flags = append(flags, "--timeout", strconv.FormatFloat(verifyTimeout, 'f', -1, 64))
	}
	if verifyWatch > 0 {
		flags = append(flags, "--watch", strconv.Itoa(verifyWatch))
	}
	if arg != "" {
		flags = append(flags, arg)
	}
	return flags
}

// watchKeys prints every key press it sees, and nothing else. It answers the
// one question a failing verify cannot: is anything readable from this device?
func watchKeys(devices []inputDevice, grab bool, seconds int) error {
	fmt.Printf("listening on:\n")
	for _, d := range devices {
		fmt.Printf("  %s  %s\n", d.Path, d.Name)
	}
	fmt.Printf("\nPress keys for %ds. Anything that arrives is printed.\n\n", seconds)

	reader, err := openReader(devices, grab)
	if err != nil {
		return err
	}
	defer reader.Close()

	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	seen := 0
	for time.Now().Before(deadline) {
		code, ok := reader.Next(time.Until(deadline))
		if !ok {
			break
		}
		seen++
		fmt.Printf("  %-4d %s\n", code, codeName(int(code)))
	}
	fmt.Println()
	if seen == 0 {
		return fmt.Errorf(
			"nothing arrived in %ds. Either no key was pressed, or this device "+
				"carries no key events — try another path from `tartarus devices`",
			seconds)
	}
	fmt.Printf("%d key press(es) seen\n", seen)
	return nil
}

// verifyPrompt says where each input physically is.
var verifyPrompt = map[string]string{
	"k01": "top row, 1st from left", "k02": "top row, 2nd",
	"k03": "top row, 3rd", "k04": "top row, 4th", "k05": "top row, 5th",
	"k06": "second row, 1st from left", "k07": "second row, 2nd",
	"k08": "second row, 3rd", "k09": "second row, 4th", "k10": "second row, 5th",
	"k11": "third row, 1st from left", "k12": "third row, 2nd",
	"k13": "third row, 3rd", "k14": "third row, 4th", "k15": "third row, 5th",
	"k16": "bottom row, 1st from left", "k17": "bottom row, 2nd",
	"k18": "bottom row, 3rd", "k19": "bottom row, 4th (last)",
	"thumb":     "round thumb button, above the pad",
	"pad_up":    "thumb pad: push UP",
	"pad_down":  "thumb pad: push DOWN",
	"pad_left":  "thumb pad: push LEFT",
	"pad_right": "thumb pad: push RIGHT",
	"bar":       "the big bar under your thumb",
}

// cmdDevices reports what verify can see and what it would read. Guessing which
// device carries the keys has been the single biggest source of wrong answers
// here, so it is worth being able to ask.
func cmdDevices() error {
	fmt.Println("keypad interfaces:")
	pads := keypadDevices(defaultDevice)
	if len(pads) == 0 {
		fmt.Printf("  none matching %s — is it plugged in?\n", defaultDevice)
	}
	for _, d := range pads {
		fmt.Printf("  %-20s %-34s %s\n", d.Path, d.Name, openness(d.Path))
	}

	fmt.Println("\nkeyd devices:")
	keydDevs := inputDevices(func(_, name string) bool {
		return strings.Contains(strings.ToLower(name), "keyd")
	})
	if len(keydDevs) == 0 {
		fmt.Println("  none — keyd creates these only while it is running")
	}
	for _, d := range keydDevs {
		fmt.Printf("  %-20s %-34s %s\n", d.Path, d.Name, openness(d.Path))
	}

	fmt.Println()
	active := ActiveProfile()
	virtual := keydVirtualDevice()
	switch {
	case active != "" && virtual != nil:
		fmt.Printf("verify would read %s (%s)\n", virtual.Path, virtual.Name)
		fmt.Printf("  because profile %q is active, so keyd owns the keypad\n", active)
	case active != "":
		fmt.Println("verify would read the keypad directly, and would find nothing:")
		fmt.Printf("  profile %q is active, so keyd holds the keypad, but no keyd\n", active)
		fmt.Println("  virtual keyboard was recognised. Pass --device with the path of")
		fmt.Println("  keyd's output device from the list above.")
	default:
		fmt.Println("verify would read the keypad directly and take it exclusively")
		fmt.Println("  because no profile is active")
	}
	return nil
}

// openness says whether the device can be read and whether something else holds
// it, which is how keyd's ownership shows up.
func openness(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return "cannot open: " + err.Error()
	}
	defer file.Close()
	if err := ioctlGrab(file, 1); err != nil {
		return "held by another process (keyd, most likely)"
	}
	_ = ioctlGrab(file, 0)
	return "readable, not held"
}
