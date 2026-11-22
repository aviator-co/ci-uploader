//go:build unix

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// Runs the CLI in a subprocess when set, so a test controls its stdio and
// signals.
const subprocessEnv = "AVIATOR_CI_TEST_SUBPROCESS"

func TestMain(m *testing.M) {
	if v := os.Getenv(subprocessEnv); v != "" {
		var args []string
		if err := json.Unmarshal([]byte(v), &args); err != nil {
			panic(err)
		}
		os.Exit(run(args))
	}
	os.Exit(m.Run())
}

func subprocess(t *testing.T, args ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0]) //nolint:gosec,noctx // re-runs this test binary
	encoded, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Env = append(os.Environ(), subprocessEnv+"="+string(encoded))
	return cmd
}

func TestRunKeepsStdoutAndStderrInOrder(t *testing.T) {
	t.Setenv("AVIATOR_API_TOKEN", "")
	cmd := subprocess(t, "run", "--check", "x", "--", "sh", "-c",
		"for i in 1 2 3 4 5 6 7 8 9 10; do echo o$i; echo e$i >&2; done")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	var want strings.Builder
	for i := 1; i <= 10; i++ {
		fmt.Fprintf(&want, "o%d\ne%d\n", i, i)
	}
	if out.String() != want.String() {
		t.Errorf("output reordered:\n%s", out.String())
	}
}

func TestRunCancelledUploadsNothing(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer srv.Close()
	t.Setenv("AVIATOR_API_HOST", srv.URL)
	t.Setenv("AVIATOR_API_TOKEN", "tok")
	t.Setenv("GIT_URL", "https://github.com/acme/app.git")
	t.Setenv("GIT_COMMIT", strings.Repeat("a", 40))

	cmd := subprocess(t, "run", "--check", "x", "--", "sh", "-c", "echo started; exec sleep 30")
	var out syncBuffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	for !strings.Contains(out.String(), "started") {
		time.Sleep(10 * time.Millisecond)
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	err := cmd.Wait()
	if code := cmd.ProcessState.ExitCode(); code != 143 {
		t.Errorf("exit code %d (%v), want 143", code, err)
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("%d API requests after a cancel", n)
	}
}
