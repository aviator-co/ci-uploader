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
	}
	err := fs.Parse(flagArgs)
	if err == nil && fs.NArg() > 0 {
		err = fmt.Errorf("unexpected argument %q", fs.Arg(0))
		fmt.Fprintf(os.Stderr, "aviator-ci: %v\n", err)
	}
	canUpload := err == nil
	if !canUpload {
		// The command still runs, so a flag this version doesn't know can't
		// fail the build.
		warnf("not uploading")
	}

	tail := capture.NewTail(upload.LogTailBytes)
	code := execute(command, tail)
	if code != 0 && canUpload {
		log, total := tail.Bytes()
		send(t, log, total)
	}
	return code
}

// execute runs command with stdout and stderr streamed through and copied into
// tail, and returns its exit code.
func execute(command []string, tail io.Writer) int {
	cmd := exec.Command(command[0], command[1:]...) //nolint:gosec,noctx // runs the user's own command, for as long as it takes
	cmd.Stdin = os.Stdin
	cmd.Stdout = io.MultiWriter(os.Stdout, tail)
	cmd.Stderr = io.MultiWriter(os.Stderr, tail)
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
			return exitNotFound
		}
		return exitNotExecutable
	}
	done := make(chan struct{})
	go func() {
		for {
			select {
			case sig := <-signals:
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
	return exitCode(cmd.ProcessState)
}
