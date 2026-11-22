// Command aviator-ci uploads a failed CI check's output to Aviator, so its
// merge queue can tell flaky failures from real ones.
package main

import (
	"fmt"
	"os"
)

const usage = `Usage: aviator-ci <command> [flags]

Commands:
  run      Run a test command and upload its output if it fails
  upload   Upload an existing log after a failed test command
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if os.Args[1] == "-h" || os.Args[1] == "--help" {
		fmt.Print(usage)
		return
	}
	fmt.Fprintf(os.Stderr, "aviator-ci: unknown command %q\n\n%s", os.Args[1], usage)
	os.Exit(2)
}
