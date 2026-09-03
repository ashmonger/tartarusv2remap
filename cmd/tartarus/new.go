package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// slugRunes are the characters a profile name may use. The slug becomes a
// filename, so anything that could escape the profiles directory is refused.
func validSlug(slug string) error {
	if slug == "" {
		return fmt.Errorf("a profile name is required, e.g. `tartarus new eldenring`")
	}
	if len(slug) > 64 {
		return fmt.Errorf("profile name is too long (%d characters, max 64)", len(slug))
	}
	for _, r := range slug {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
		default:
			return fmt.Errorf("profile name %q may only use lowercase letters, digits, dot, dash and underscore", slug)
		}
	}
	if strings.HasPrefix(slug, ".") {
		return fmt.Errorf("profile name may not start with a dot")
	}
	return nil
}

// newProfileSkeleton is what a from-scratch profile starts as. It documents the
// key names in place, because that is where someone editing by hand looks.
func newProfileSkeleton(slug, name, extends string) string {
	var b strings.Builder
	b.WriteString("[profile]\n")
	fmt.Fprintf(&b, "name = %s\n", name)
	if extends != "" {
		fmt.Fprintf(&b, "extends = %s\n", extends)
	}
	b.WriteString(`
# Bind keys with ` + "`tartarus edit " + slug + "`" + `, or by hand below.
#
#   k01 k02 k03 k04 k05     the 19 backlit keys, in reading order
#   k06 k07 k08 k09 k10
#   k11 k12 k13 k14 k15
#   k16 k17 k18 k19
#
#   thumb                   Hyperesponse thumb key
#   bar                     spacebar actuator
#   pad = w s a d           8-way thumb pad: up down left right
#
# A key you do not name sends nothing.`)
	if extends != "" {
		fmt.Fprintf(&b, "\n# Use `off` to drop a binding inherited from %s.", extends)
	}
	b.WriteString(`
# Never use ` + "`0`" + ` to disable a key — that is the digit zero.
# ` + "`tartarus keys`" + ` lists all 138 bindable keyboard keys.

[keys]
`)
	return b.String()
}

// retitle replaces the name in a copied profile, so a clone does not keep
// announcing itself as the profile it came from.
func retitle(body, name string) string {
	lines := strings.Split(body, "\n")
	section := ""
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			section = strings.ToLower(strings.TrimSpace(trimmed[1 : len(trimmed)-1]))
			continue
		}
		if section != "profile" {
			continue
		}
		if key, _, found := strings.Cut(trimmed, "="); found && strings.TrimSpace(strings.ToLower(key)) == "name" {
			lines[i] = "name = " + name
			return strings.Join(lines, "\n")
		}
	}
	// No name to replace, so introduce one right after [profile].
	for i, line := range lines {
		if strings.EqualFold(strings.TrimSpace(line), "[profile]") {
			rest := append([]string{"name = " + name}, lines[i+1:]...)
			return strings.Join(append(lines[:i+1], rest...), "\n")
		}
	}
	return "[profile]\nname = " + name + "\n\n" + body
}

// newProfileTarget decides where `new` writes.
//
// This is deliberately not a search. Where a profile lands must not depend on
// the current directory: a relative `profiles` directory happening to exist is
// no reason to put someone's new profile in it.
func newProfileTarget(dirs []string) (string, error) {
	var target string
	switch {
	case profilesExplicit && len(dirs) > 0:
		target = dirs[0] // --profiles named it
	case os.Getenv("TARTARUS_PROFILES") != "":
		target = os.Getenv("TARTARUS_PROFILES")
	default:
		var err error
		if target, err = UserProfileDir(); err != nil {
			return "", err
		}
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return "", fmt.Errorf("cannot create %s: %w", target, err)
	}
	return target, nil
}

// CreateProfile writes a new profile and reports where it landed.
//
// Without `from`, the profile extends default.profile when one exists, so a new
// game starts with esc, the map key and WASD on the thumb pad already bound.
// With `from`, the named profile's file is copied as a starting point.
func CreateProfile(slug, from, name string, dirs []string) (string, error) {
	if err := validSlug(slug); err != nil {
		return "", err
	}
	if existing, err := findProfile(slug, dirs); err == nil {
		return "", fmt.Errorf("profile %q already exists at %s", slug, existing)
	}
	if name == "" {
		name = slug
	}

	var body string
	if from != "" {
		if from == slug {
			return "", fmt.Errorf("cannot copy %q onto itself", slug)
		}
		sourcePath, err := findProfile(from, dirs)
		if err != nil {
			return "", fmt.Errorf("cannot copy from %q: %w", from, err)
		}
		data, err := os.ReadFile(sourcePath)
		if err != nil {
			return "", err
		}
		body = retitle(string(data), name)
		if !strings.HasSuffix(body, "\n") {
			body += "\n"
		}
	} else {
		extends := ""
		if _, err := findProfile("default", dirs); err == nil && slug != "default" {
			extends = "default"
		}
		body = newProfileSkeleton(slug, name, extends)
	}

	dir, err := newProfileTarget(dirs)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, slug+profileSuffix)
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("%s already exists", path)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return "", err
	}

	// A profile that cannot be loaded back is worse than no profile at all.
	// Validation searches the whole path, not just where the file landed: the
	// profile it extends usually lives elsewhere, such as the packaged
	// default.profile under /usr/share.
	if _, err := LoadProfile(slug, append([]string{dir}, dirs...)); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("the new profile did not validate, so it was removed: %w", err)
	}
	return path, nil
}
