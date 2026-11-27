package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"slices"
	"sync/atomic"
	"time"

	"github.com/aviator-co/ci-uploader/internal/capture"
	"github.com/aviator-co/ci-uploader/internal/upload"
)

// Shell conventions, for a command that never ran.
const (
	exitNotExecutable = 126
	exitNotFound      = 127
)

func runRun(args []string) int {
	sep := slices.Index(args, "--")
	if sep < 0 || sep == len(args)-1 {
		fmt.Fprint(os.Stderr, "Usage: aviator-ci run --check <name> [flags] -- <command> [args...]\n")
		return 2
	}
	flagArgs, command := args[:sep], args[sep+1:]

	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	var t target
	t.register(fs)
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), "Usage: aviator-ci run --check <name> [flags] -- <command> [args...]\n\n")
		fs.PrintDefaults()
		fmt.Fprint(fs.Output(), "\nA signal to aviator-ci counts as a cancel and uploads nothing, so put a timeout\ninside it: aviator-ci run ... -- timeout 30m make test\n")
	}
	err := fs.Parse(flagArgs)
	if err == nil && fs.NArg() > 0 {
		err = fmt.Errorf("unexpected argument %q", fs.Arg(0))
		fmt.Fprintf(os.Stderr, "aviator-ci: %v\n", err)
	}
	if err == nil {
		if err := t.checkQuarantine(); err != nil {
			fmt.Fprintf(os.Stderr, "aviator-ci: %v\n", err)
			return 2
		}
	}
	canUpload := err == nil
	if !canUpload {
		// The command still runs, so a flag this version doesn't know can't
		// fail the build.
		warnf("not uploading")
	}

	tail := capture.NewTail(upload.LogTailBytes)
	// Truncated: some filesystems keep whole seconds, and a report written in
	// the same second must not read as stale.
	started := time.Now().Truncate(time.Second)
	code, stopped := execute(command, tail)
	if stopped {
		// The build was cancelled, not failed.
		warnf("stopped by a signal; not uploading")
		return code
	}
	if !canUpload {
		return code
	}
	switch {
	case code != 0:
		log, total := tail.Bytes()
		result := send(t, outcome{log: log, logSize: total, exitCode: code, started: started})
		return applyQuarantine(t, code, result)
	case len(t.junit) > 0:
		// Passing runs are half of what tells a flaky test from a broken one.
		send(t, outcome{exitCode: code, started: started})
	}
	return code
}

// execute runs command with stdout and stderr streamed through and copied into
// tail. It returns the exit code, and whether aviator-ci was signalled to stop.
func execute(command []string, tail io.Writer) (int, bool) {
	cmd := exec.Command(command[0], command[1:]...) //nolint:gosec,noctx // runs the user's own command, for as long as it takes
	cmd.Stdin = os.Stdin
	if sameFile(os.Stdout, os.Stderr) {
		// One writer gives the command one pipe for both, which keeps their
		// lines in order, as they would be without aviator-ci.
		w := io.MultiWriter(os.Stdout, tail)
		cmd.Stdout, cmd.Stderr = w, w
	} else {
		cmd.Stdout = io.MultiWriter(os.Stdout, tail)
		cmd.Stderr = io.MultiWriter(os.Stderr, tail)
	}
	// A background process the command left running can hold the output pipes
	// open; don't wait on it once the command itself has exited.
	cmd.WaitDelay = 10 * time.Second

	// Caught so aviator-ci outlives the command and reports its exit code, and
	// passed on for runners that signal only the process they started.
	signals := make(chan os.Signal, 4)
	signal.Notify(signals, forwardedSignals...)
	defer signal.Stop(signals)

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "aviator-ci: %v\n", err)
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			return exitNotFound, false
		}
		return exitNotExecutable, false
	}
	var stopped atomic.Bool
	done := make(chan struct{})
	go func() {
		for {
			select {
			case sig := <-signals:
				stopped.Store(true)
				_ = cmd.Process.Signal(sig)
			case <-done:
				return
			}
		}
	}()
	err := cmd.Wait()
	close(done)

	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		// The output copy failed; the command itself still exited.
		warnf("%v", err)
	}
	return exitCode(cmd.ProcessState), stopped.Load()
}

func sameFile(a, b *os.File) bool {
	ai, err := a.Stat()
	if err != nil {
		return false
	}
	bi, err := b.Stat()
	return err == nil && os.SameFile(ai, bi)
}
