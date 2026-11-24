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
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aviator-co/ci-uploader/internal/api"
)

// fakeAviator serves the upload API and the signed URLs it hands out.
type fakeAviator struct {
	t        *testing.T
	skipped  *string
	refuse   map[int64]bool // PUTs answered 403 without storing
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
				resp.Files = append(resp.Files, api.Slot{ID: id, Type: d.Type, UploadURL: (*srv).URL + "/put/" + itoa(id)})
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
		f.stored[id] = body
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
	if err := Run(t.Context(), client, request(t, "<testsuite/>"), t.Logf); err != nil {
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
	if err := Run(t.Context(), client, request(t), t.Logf); err != nil {
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
	if err := Run(t.Context(), client, request(t), t.Logf); err != nil {
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
	if err := Run(t.Context(), client, req, t.Logf); err != nil {
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
