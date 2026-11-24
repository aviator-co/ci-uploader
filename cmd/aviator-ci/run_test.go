//go:build unix

package main

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeAPI records the log run uploads.
func fakeAPI(t *testing.T) *string {
	t.Helper()
	var mu sync.Mutex
	var uploaded string
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/ci/uploads":
			_, _ = io.WriteString(w, `{"skipped":null,"content_type":"application/gzip","files":[{"id":1,"type":"log","upload_url":"`+srv.URL+`/put"}]}`)
		case "/put":
			zr, err := gzip.NewReader(r.Body)
			if err != nil {
				t.Error(err)
				return
			}
			data, _ := io.ReadAll(zr)
			mu.Lock()
			uploaded = string(data)
			mu.Unlock()
		case "/api/v1/ci/uploads/complete":
			_ = json.NewEncoder(w).Encode(map[string][]int{"uploaded": {1}})
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("AVIATOR_API_HOST", srv.URL)
	t.Setenv("AVIATOR_API_TOKEN", "tok")
	t.Setenv("GIT_URL", "https://github.com/acme/app.git")
	t.Setenv("GIT_COMMIT", strings.Repeat("a", 40))
	t.Setenv("BUILD_URL", "https://jenkins/job/app/1/")
	return &uploaded
}

func TestRunUploadsOutputOnFailure(t *testing.T) {
	uploaded := fakeAPI(t)
	code := run([]string{"run", "--check", "ci/tests", "--", "sh", "-c", "echo out; echo err >&2; exit 3"})
	if code != 3 {
		t.Errorf("exit code %d, want 3", code)
	}
	if !strings.Contains(*uploaded, "out\n") || !strings.Contains(*uploaded, "err\n") {
		t.Errorf("uploaded %q", *uploaded)
	}
}

func TestRunPassingCommandUploadsNothing(t *testing.T) {
	uploaded := fakeAPI(t)
	if code := run([]string{"run", "--check", "ci/tests", "--", "sh", "-c", "echo ok"}); code != 0 {
		t.Errorf("exit code %d", code)
	}
	if *uploaded != "" {
		t.Errorf("uploaded %q", *uploaded)
	}
}

func TestRunExitCodes(t *testing.T) {
	t.Setenv("AVIATOR_API_TOKEN", "")
	for _, tc := range []struct {
		args []string
		want int
	}{
		{[]string{"run", "--check", "x", "--", "sh", "-c", "kill -TERM $$"}, 143},
		{[]string{"run", "--check", "x", "--", "/nonexistent/cmd"}, 127},
		// An unknown flag still runs the command and keeps its result.
		{[]string{"run", "--newer-flag", "--", "sh", "-c", "exit 0"}, 0},
		{[]string{"run", "--newer-flag", "--", "sh", "-c", "exit 4"}, 4},
		{[]string{"run", "--check", "x"}, 2},
		{[]string{"run", "--check", "x", "--"}, 2},
	} {
		if got := run(tc.args); got != tc.want {
			t.Errorf("run(%q) = %d, want %d", tc.args, got, tc.want)
		}
	}
}

func TestRunDoesNotWaitOnBackgroundProcesses(t *testing.T) {
	t.Setenv("AVIATOR_API_TOKEN", "")
	start := time.Now()
	if code := run([]string{"run", "--check", "x", "--", "sh", "-c", "sleep 30 & exit 5"}); code != 5 {
		t.Errorf("exit code %d", code)
	}
	if d := time.Since(start); d > 20*time.Second {
		t.Errorf("took %v", d)
	}
}
