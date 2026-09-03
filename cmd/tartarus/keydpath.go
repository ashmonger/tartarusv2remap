package main

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// keydBinary is resolved once; "" means keyd could not be found.
var keydBinary = findKeyd()

// execStartPath pulls the binary out of systemd's ExecStart, which reads
// `{ path=/usr/local/bin/keyd ; argv[]=... }`.
var execStartPath = regexp.MustCompile(`path=([^\s;]+)`)

// findKeyd locates the keyd binary.
//
// PATH alone is not enough. keyd's own Makefile installs to /usr/local/bin,
// which is absent from the PATH dpkg gives maintainer scripts, and a daemon
// installed outside the package manager can live anywhere. So when PATH fails,
// systemd is asked where the unit's binary is — it knows, because it started it.
func findKeyd() string {
	if override := os.Getenv("TARTARUS_KEYD"); override != "" {
		return override
	}
	if path, err := exec.LookPath("keyd"); err == nil {
		return path
	}
	if out, err := exec.Command(
		"systemctl", "show", "keyd.service", "-p", "ExecStart", "--value",
	).Output(); err == nil {
		if m := execStartPath.FindSubmatch(out); m != nil {
			if path := strings.TrimSpace(string(m[1])); executable(path) {
				return path
			}
		}
	}
	for _, path := range []string{
		"/usr/local/bin/keyd",
		"/usr/bin/keyd",
		"/usr/sbin/keyd",
		"/usr/local/sbin/keyd",
		"/opt/keyd/bin/keyd",
	} {
		if executable(path) {
			return path
		}
	}
	return ""
}

func executable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
}

// keyd builds a command for the keyd binary wherever it was found.
func keyd(args ...string) *exec.Cmd {
	if keydBinary == "" {
		return exec.Command("keyd", args...) // will fail loudly
	}
	return exec.Command(keydBinary, args...)
}
