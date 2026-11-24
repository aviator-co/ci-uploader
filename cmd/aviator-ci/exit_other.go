//go:build !unix

package main

import "os"

var forwardedSignals = []os.Signal{os.Interrupt}

func exitCode(state *os.ProcessState) int {
	return state.ExitCode()
}
