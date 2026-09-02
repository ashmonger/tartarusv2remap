package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// ImportMap converts a hand-written udev hwdb .map file into a profile,
// returning the profile text and any notes about what it had to interpret.
//
// The interesting case is `=0`. An hwdb value is a Linux key name with KEY_
// stripped, and `0` is KEY_0 — the digit zero — so a key written that way types
// a zero rather than doing nothing. Every .map file used it to mean "unbound",
// which is how it was meant, so that is how it is read here. An empty value is
// dropped by udev entirely and gets the same treatment.
func ImportMap(path string) (profile string, notes []string, err error) {
	file, err := os.Open(path)
	if err != nil {
		return "", nil, err
	}
	defer file.Close()

	labelOf := make(map[string]string, len(layout))
	for _, in := range layout {
		labelOf[in.Scancode] = in.Label
	}

	bindings := map[string]string{}
	device := ""
	scanner := bufio.NewScanner(file)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		if strings.HasPrefix(text, "evdev:") {
			device = text
			continue
		}
		if !strings.HasPrefix(text, "KEYBOARD_KEY_") {
			continue
		}
		prop, value, found := strings.Cut(text, "=")
		if !found {
			notes = append(notes, fmt.Sprintf("line %d: no value, skipped: %q", line, text))
			continue
		}
		scancode := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(prop), "KEYBOARD_KEY_"))
		label, known := labelOf[scancode]
		if !known {
			notes = append(notes, fmt.Sprintf("line %d: scancode %s is not on this keypad, skipped", line, scancode))
			continue
		}
		value = strings.ToLower(strings.TrimSpace(value))
		switch value {
		case "0":
			notes = append(notes, fmt.Sprintf("%s: `0` read as unbound (as written it sends the digit zero)", label))
			value = "off"
		case "":
			notes = append(notes, fmt.Sprintf("%s: empty value read as unbound (udev drops it)", label))
			value = "off"
		case "reserved", "unknown":
			value = "off"
		}
		bindings[label] = value
	}
	if err := scanner.Err(); err != nil {
		return "", notes, err
	}
	if len(bindings) == 0 {
		return "", notes, fmt.Errorf("%s: found no KEYBOARD_KEY_ lines", path)
	}

	var b strings.Builder
	b.WriteString("[profile]\n")
	fmt.Fprintf(&b, "name = %s\n", strings.TrimSuffix(baseName(path), ".map"))
	if id := deviceFromModalias(device); id != "" && id != defaultDevice {
		fmt.Fprintf(&b, "device = %s\n", id)
	}
	b.WriteString("\n[keys]\n")
	for _, in := range layout {
		// Unbound is the default, so those lines simply disappear.
		if value, ok := bindings[in.Label]; ok && !isOff(value) {
			fmt.Fprintf(&b, "%s = %s\n", in.Label, value)
		}
	}
	return b.String(), notes, nil
}

// deviceFromModalias pulls the vendor:product out of an hwdb match line such as
// `evdev:input:b0003v1532p022B*`.
func deviceFromModalias(line string) string {
	i := strings.LastIndex(line, "v")
	if i < 0 {
		return ""
	}
	rest := line[i+1:]
	vendor, product, found := strings.Cut(rest, "p")
	if !found {
		return ""
	}
	product = strings.TrimSuffix(product, "*")
	if len(vendor) != 4 || len(product) != 4 {
		return ""
	}
	return strings.ToLower(vendor) + ":" + strings.ToLower(product)
}

func baseName(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}
