package main

import (
	"fmt"
	"runtime"
	"strings"
)

// Set at build time by the Makefile. The defaults are what a plain `go build`
// produces, and say so rather than pretending to be a release.
var (
	version    = "dev"
	commit     = "unknown"
	commitDate = "unknown"
)

// printVersion reports what this binary is and what it can see.
//
// The build identity answers "is this the copy I just installed?", which is
// otherwise guesswork across machines. The rest is included because every
// problem worth debugging so far has come down to which keyd was found and
// which profiles were in scope.
func printVersion(dirs []string) {
	fmt.Printf("tartarus %s\n", version)
	fmt.Printf("  commit     %s\n", commit)
	fmt.Printf("  committed  %s\n", commitDate)
	fmt.Printf("  go         %s\n", runtime.Version())

	if keydBinary == "" {
		fmt.Printf("  keyd       not found\n")
	} else {
		checks := "no `check` subcommand"
		if keydSupportsCheck() {
			checks = "supports `check`"
		}
		fmt.Printf("  keyd       %s (%s)\n", keydBinary, checks)
	}

	if active := ActiveProfile(); active == "" {
		fmt.Printf("  active     none\n")
	} else {
		fmt.Printf("  active     %s\n", active)
	}

	found := ListProfiles(dirs)
	fmt.Printf("  profiles   %d found\n", len(found))
	for _, dir := range dirs {
		marker := " "
		if n := len(ListProfiles([]string{dir})); n > 0 {
			marker = "*"
		}
		fmt.Printf("    %s %s\n", marker, dir)
	}
	if len(found) > 0 {
		fmt.Printf("             %s\n", strings.Join(found, " "))
	}
}
