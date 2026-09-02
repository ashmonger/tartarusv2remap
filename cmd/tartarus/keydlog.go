package main

import (
	"os/exec"
	"strings"
	"time"
)

// keydErrorsSince returns the errors and warnings keyd logged after a moment.
//
// Only errors are treated as fatal. Rolling a config back over a warning would
// be worse than the problem it guards against.
//
// This stands in for `keyd check` on versions that do not have it. Without it a
// reload reports success while keyd rejects every binding in the file, leaving a
// config that is loaded, matched, and completely inert — which is the worst way
// for this to fail, because nothing says so.
func keydErrorsSince(when time.Time) (errs, warns []string) {
	if _, err := exec.LookPath("journalctl"); err != nil {
		return nil, nil
	}
	out, err := exec.Command(
		"journalctl", "-u", "keyd",
		"--since", when.Add(-2*time.Second).Format("2006-01-02 15:04:05"),
		"--no-pager", "-o", "cat",
	).Output()
	if err != nil {
		return nil, nil
	}
	for _, line := range strings.Split(string(out), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		switch upper := strings.ToUpper(trimmed); {
		case strings.Contains(upper, "ERROR"):
			errs = append(errs, trimmed)
		case strings.Contains(upper, "WARNING"):
			warns = append(warns, trimmed)
		}
	}
	return errs, warns
}

// summarise keeps a long list of near-identical parse errors readable.
func summarise(problems []string, limit int) string {
	if len(problems) <= limit {
		return "  " + strings.Join(problems, "\n  ")
	}
	shown := problems[:limit]
	return "  " + strings.Join(shown, "\n  ") +
		"\n  ... and " + itoa(len(problems)-limit) + " more"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
