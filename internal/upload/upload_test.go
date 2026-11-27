package upload

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aviator-co/ci-uploader/internal/api"
)

// fakeAviator serves the upload API and the signed URLs it hands out.
type fakeAviator struct {
	t       *testing.T
	skipped *string
	// Indexes of the declared files to hand out URLs for; nil for all, with
	// no index, as an older Aviator does.
	want     []int
	refuse   map[int64]bool // PUTs answered 403 without storing
	lose     map[int64]bool // PUTs answered 200 and never stored
	mu       sync.Mutex
	declared api.CreateRequest
	stored   map[int64][]byte
	puts     map[int64]int
	confirms [][]int64
}

func (f *fakeAviator) handler(srv **httptest.Server) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/ci/uploads", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if err := json.NewDecoder(r.Body).Decode(&f.declared); err != nil {
			f.t.Fatal(err)
		}
		resp := api.CreateResponse{Skipped: f.skipped, ContentType: "application/gzip"}
		if f.skipped == nil {
			for i, d := range f.declared.Files {
				id := int64(100 + i)
				s := api.Slot{ID: id, Type: d.Type, UploadURL: (*srv).URL + "/put/" + itoa(id)}
				if f.want != nil {
					if !slices.Contains(f.want, i) {
						continue
					}
					s.Index = &i
				}
				resp.Files = append(resp.Files, s)
			}
		}
		_ = json.NewEncoder(w).Encode(resp)
	})
	mux.HandleFunc("PUT /put/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := atoi(r.PathValue("id"))
		if r.Header.Get("Content-Type") != "application/gzip" {
			f.t.Errorf("PUT content type %q", r.Header.Get("Content-Type"))
		}
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.puts[id]++
		if f.refuse[id] {
			delete(f.refuse, id)
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if !f.lose[id] {
			f.stored[id] = body
		}
	})
	mux.HandleFunc("POST /api/v1/ci/uploads/complete", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ IDs []int64 }
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.confirms = append(f.confirms, req.IDs)
		resp := api.CompleteResponse{}
		for _, id := range req.IDs {
			if _, ok := f.stored[id]; ok {
				resp.Uploaded = append(resp.Uploaded, id)
			} else {
				resp.Missing = append(resp.Missing, id)
			}
		}
		_ = json.NewEncoder(w).Encode(resp)
	})
	return mux
}

func newFake(t *testing.T) (*fakeAviator, *api.Client) {
	t.Helper()
	f := &fakeAviator{t: t, refuse: map[int64]bool{}, stored: map[int64][]byte{}, puts: map[int64]int{}}
	var srv *httptest.Server
	srv = httptest.NewServer(f.handler(&srv))
	t.Cleanup(srv.Close)
	client := api.New(srv.URL, "tok", "test")
	client.Backoff = []time.Duration{0, 0}
	return f, client
}

func gunzip(t *testing.T, data []byte) string {
	t.Helper()
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func request(t *testing.T, junit ...string) Request {
	t.Helper()
	var paths []string
	for i, content := range junit {
		p := filepath.Join(t.TempDir(), "r"+itoa(int64(i))+".xml")
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	return Request{
		Repo:       "acme/app",
		Commit:     strings.Repeat("a", 40),
		Check:      "ci/tests",
		BuildURL:   "https://jenkins/job/app/42/",
		Log:        []byte("FAIL TestThing\n"),
		JUnitPaths: paths,
	}
}

func TestRunUploadsAndConfirms(t *testing.T) {
	f, client := newFake(t)
	if _, err := Run(t.Context(), client, request(t, "<testsuite/>"), t.Logf); err != nil {
		t.Fatal(err)
	}

	d := f.declared
	if d.Repo != "acme/app" || d.CheckName != "ci/tests" || d.BuildURL != "https://jenkins/job/app/42/" {
		t.Errorf("declared %+v", d)
	}
	if len(d.Files) != 2 || d.Files[0].Type != api.FileTypeLog || d.Files[1].Type != api.FileTypeJUnit {
		t.Fatalf("declared files %+v", d.Files)
	}
	if got := gunzip(t, f.stored[100]); got != "FAIL TestThing\n" {
		t.Errorf("log = %q", got)
	}
	if got := gunzip(t, f.stored[101]); got != "<testsuite/>" {
		t.Errorf("junit = %q", got)
	}
	if int(d.Files[0].SizeBytes) != len(f.stored[100]) {
		t.Errorf("declared %d bytes, stored %d", d.Files[0].SizeBytes, len(f.stored[100]))
	}
	if len(f.confirms) != 1 || len(f.confirms[0]) != 2 {
		t.Errorf("confirms = %v", f.confirms)
	}
}

func TestRunSkipped(t *testing.T) {
	f, client := newFake(t)
	reason := "not a draft PR"
	f.skipped = &reason
	if _, err := Run(t.Context(), client, request(t), t.Logf); err != nil {
		t.Fatal(err)
	}
	if len(f.puts) != 0 || len(f.confirms) != 0 {
		t.Errorf("puts %v, confirms %v", f.puts, f.confirms)
	}
}

// A 403 means a retried PUT already landed, or didn't; /complete settles it
// and anything missing is sent again.
func TestRunReuploadsWhatIsMissing(t *testing.T) {
	f, client := newFake(t)
	f.refuse[100] = true
	if _, err := Run(t.Context(), client, request(t), t.Logf); err != nil {
		t.Fatal(err)
	}
	if f.puts[100] != 2 {
		t.Errorf("PUT %d times, want 2", f.puts[100])
	}
	if len(f.confirms) != 2 || len(f.confirms[1]) != 1 || f.confirms[1][0] != 100 {
		t.Errorf("confirms = %v", f.confirms)
	}
	if gunzip(t, f.stored[100]) == "" {
		t.Error("log not stored")
	}
}

func TestRunNothingToUpload(t *testing.T) {
	f, client := newFake(t)
	req := request(t)
	req.Log = nil
	if _, err := Run(t.Context(), client, req, t.Logf); err != nil {
		t.Fatal(err)
	}
	if f.declared.CheckName != "" {
		t.Error("declared uploads with no files")
	}
}

func TestGzipLogFitsTheCap(t *testing.T) {
	// Random-ish bytes barely compress, so the tail has to shrink.
	text := make([]byte, 30<<20)
	x := uint32(1)
	for i := range text {
		x = x*1664525 + 1013904223
		text[i] = byte(x >> 24)
	}
	gz, err := gzipLog(text[len(text)-LogTailBytes:], int64(len(text)))
	if err != nil {
		t.Fatal(err)
	}
	if len(gz) > MaxFileBytes {
		t.Errorf("compressed log is %d bytes", len(gz))
	}
	if note := gunzip(t, gz)[:80]; !strings.Contains(note, " of 31457280 bytes]") {
		t.Errorf("note = %q", note)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func atoi(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

func TestRunSendsTheFailedTests(t *testing.T) {
	f, client := newFake(t)
	report := `<testsuite name="s"><testcase classname="a" name="ok"/>` +
		`<testcase classname="a" name="bad"><failure message="nope"/></testcase></testsuite>`
	result, err := Run(t.Context(), client, request(t, report, "not xml"), t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.declared.FailedTests) != 1 || f.declared.FailedTests[0].Name != "bad" {
		t.Errorf("failed tests %+v", f.declared.FailedTests)
	}
	if f.declared.JUnitComplete == nil || *f.declared.JUnitComplete || result.JUnitComplete {
		t.Error("a report that doesn't parse should make the run incomplete")
	}
}

func TestRunLeavesOutStaleReports(t *testing.T) {
	f, client := newFake(t)
	req := request(t, "<testsuite/>")
	req.Since = time.Now().Add(time.Hour)
	result, err := Run(t.Context(), client, req, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.declared.Files) != 1 || f.declared.Files[0].Type != api.FileTypeLog {
		t.Errorf("declared %+v", f.declared.Files)
	}
	if result.JUnitComplete {
		t.Error("a stale report should make the run incomplete")
	}
}

func TestRunCompleteness(t *testing.T) {
	f, client := newFake(t)
	report := `<testsuite name="s"><testcase classname="a" name="ok"/></testsuite>`
	for _, tc := range []struct {
		name     string
		since    time.Time
		missing  bool
		complete bool
	}{
		{"start time known", time.Now().Add(-time.Hour), false, true},
		{"start time unknown", time.Time{}, false, false},
		{"a pattern matched nothing", time.Now().Add(-time.Hour), true, false},
	} {
		req := request(t, report)
		req.Since = tc.since
		req.JUnitMissing = tc.missing
		result, err := Run(t.Context(), client, req, t.Logf)
		if err != nil {
			t.Fatal(err)
		}
		if result.JUnitComplete != tc.complete || *f.declared.JUnitComplete != tc.complete {
			t.Errorf("%s: complete = %v", tc.name, result.JUnitComplete)
		}
	}
}

func TestRunSendsEachDistinctFailureUpToTheLimit(t *testing.T) {
	f, client := newFake(t)
	var cases strings.Builder
	// The same test failing two ways, as a retry plugin reports it, then more
	// failures than Aviator accepts.
	cases.WriteString(`<testcase classname="a" name="retried"><failure message="timeout"/></testcase>`)
	cases.WriteString(`<testcase classname="a" name="retried"><failure message="assertion"/></testcase>`)
	for i := range MaxFailedTests {
		cases.WriteString(`<testcase classname="a" name="t` + itoa(int64(i)) + `"><failure/></testcase>`)
	}
	req := request(t, `<testsuite name="s">`+cases.String()+`</testsuite>`)
	req.Since = time.Now().Add(-time.Hour)
	result, err := Run(t.Context(), client, req, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.declared.FailedTests) != MaxFailedTests {
		t.Errorf("sent %d failed tests", len(f.declared.FailedTests))
	}
	if f.declared.FailedTests[1].Message != "assertion" {
		t.Errorf("second failure of the retried test was dropped: %+v", f.declared.FailedTests[:2])
	}
	if result.JUnitComplete {
		t.Error("going past the limit should make the run incomplete")
	}
}

func TestRunUploadsOnlyTheFilesAviatorWants(t *testing.T) {
	f, client := newFake(t)
	f.want = []int{1}
	req := request(t, `<testsuite><testcase classname="a" name="b"/></testsuite>`)
	req.ExitCode = 1
	if _, err := Run(t.Context(), client, req, t.Logf); err != nil {
		t.Fatal(err)
	}
	if len(f.stored) != 1 || f.stored[101] == nil {
		t.Errorf("stored %v, want only the report", f.stored)
	}
}

func TestRunSendsTheRunsTimes(t *testing.T) {
	f, client := newFake(t)
	req := request(t)
	req.Since = time.Unix(1700000000, 0)
	req.Finished = req.Since.Add(time.Minute)
	if _, err := Run(t.Context(), client, req, t.Logf); err != nil {
		t.Fatal(err)
	}
	if f.declared.StartedAt == nil || !f.declared.StartedAt.Equal(req.Since) ||
		f.declared.FinishedAt == nil || !f.declared.FinishedAt.Equal(req.Finished) {
		t.Errorf("times %v %v", f.declared.StartedAt, f.declared.FinishedAt)
	}
}

func TestRunCountsTestCasesAndConfirmsReports(t *testing.T) {
	report := `<testsuite name="s"><testcase classname="a" name="x"/><testcase name="y"><failure/></testcase></testsuite>`
	f, client := newFake(t)
	req := request(t, report, report)
	req.ExitCode = 1
	result, err := Run(t.Context(), client, req, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	if result.TestCount != 2 || f.declared.TestCount == nil || *f.declared.TestCount != 2 {
		t.Errorf("test count %d, declared %v", result.TestCount, f.declared.TestCount)
	}
	if !result.ReportsConfirmed {
		t.Error("reports not confirmed")
	}

	f, client = newFake(t)
	f.lose = map[int64]bool{102: true}
	if result, err = Run(t.Context(), client, req, t.Logf); err != nil {
		t.Fatal(err)
	}
	if result.ReportsConfirmed {
		t.Error("a lost report counted as confirmed")
	}
}
