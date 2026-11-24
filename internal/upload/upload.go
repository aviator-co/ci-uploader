// Package upload sends a check's test reports, and a failed check's log, to
// Aviator.
package upload

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"
	"unicode/utf8"

	"github.com/aviator-co/ci-uploader/internal/api"
	"github.com/aviator-co/ci-uploader/internal/junit"
)

const (
	// Aviator's limits on each gzipped file and on the number of reports.
	MaxFileBytes  = 10 << 20
	MaxJUnitFiles = 100
	// Aviator's limits on the failed tests sent with an upload. A run that
	// goes past them can't be quarantined, so it's marked incomplete.
	MaxFailedTests   = 1000
	MaxTestNameChars = 4096

	// The end of a log is where failures are, so a longer one keeps its tail.
	// Well under Aviator's 40 MiB cap on a decompressed log.
	LogTailBytes = 16 << 20

	// Confirming gets its own budget, so slow report uploads that use up the
	// caller's can't leave an uploaded log unconfirmed and never read.
	confirmTimeout = 20 * time.Second
)

type Request struct {
	Repo     string
	Commit   string
	Check    string
	BuildURL string
	Branch   string
	Variant  string
	ExitCode int
	// The end of the log, and the whole log's size if that's longer.
	Log        []byte
	LogSize    int64
	JUnitPaths []string
	// A --junit pattern was invalid or matched nothing, so a report may be
	// missing.
	JUnitMissing bool
	// When the test command started. Reports older than this are left out as
	// stale. Zero when unknown, which leaves the reports incomplete.
	Since time.Time
}

// Result is what the reports said and what Aviator answered.
type Result struct {
	// Each distinct failure, up to MaxFailedTests.
	Failures []junit.Failure
	// Every report was found, parsed, and written after Since, and every
	// failure fits what Aviator accepts.
	JUnitComplete bool
	Quarantine    *api.Quarantine
}

type Logf func(format string, args ...any)

type file struct {
	typ  api.FileType
	name string
	gz   []byte
}

// Run uploads the files in req. Nothing is uploaded when Aviator won't read
// them, which isn't an error. The result's report fields are set even when
// the upload fails.
func Run(ctx context.Context, client *api.Client, req Request, logf Logf) (Result, error) {
	files, result, err := prepare(req, logf)
	if err != nil {
		return result, err
	}
	if len(files) == 0 {
		logf("nothing to upload")
		return result, nil
	}

	declared := make([]api.DeclaredFile, len(files))
	for i, f := range files {
		declared[i] = api.DeclaredFile{Type: f.typ, SizeBytes: int64(len(f.gz))}
	}
	create := api.CreateRequest{
		Repo:        req.Repo,
		CommitSHA:   req.Commit,
		CheckName:   req.Check,
		BuildURL:    req.BuildURL,
		Files:       declared,
		Branch:      req.Branch,
		Variant:     req.Variant,
		ExitCode:    &req.ExitCode,
		FailedTests: result.Failures,
	}
	if len(req.JUnitPaths) > 0 {
		create.JUnitComplete = &result.JUnitComplete
	}
	created, err := client.CreateUploads(ctx, create)
	if err != nil {
		return result, fmt.Errorf("declaring uploads: %w", err)
	}
	result.Quarantine = created.Quarantine
	if created.Skipped != nil {
		logf("not uploading: %s", *created.Skipped)
		return result, nil
	}
	if len(created.Files) != len(files) {
		return result, fmt.Errorf("asked for %d upload URLs, got %d", len(files), len(created.Files))
	}

	// Slots come back in the order the files were declared.
	byID := make(map[int64]int, len(files))
	ids := make([]int64, len(files))
	for i, slot := range created.Files {
		byID[slot.ID] = i
		ids[i] = slot.ID
	}
	put := func(i int) error {
		err := client.Put(ctx, created.Files[i].UploadURL, created.ContentType, files[i].gz)
		// Storage is write-once, so a retried PUT whose first attempt landed is
		// refused; /complete tells us whether it's there.
		var apiErr *api.Error
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusForbidden {
			return nil
		}
		return err
	}
	for i := range files {
		if err := put(i); err != nil {
			logf("could not upload %s: %v", files[i].name, err)
		}
	}

	confirm := func(ids []int64) (*api.CompleteResponse, error) {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), confirmTimeout)
		defer cancel()
		return client.CompleteUploads(ctx, ids)
	}
	done, err := confirm(ids)
	if err != nil {
		return result, fmt.Errorf("confirming uploads: %w", err)
	}
	if len(done.Missing) > 0 {
		for _, id := range done.Missing {
			if err := put(byID[id]); err != nil {
				logf("could not upload %s: %v", files[byID[id]].name, err)
			}
		}
		if done, err = confirm(done.Missing); err != nil {
			return result, fmt.Errorf("confirming uploads: %w", err)
		}
	}
	for _, id := range done.Missing {
		logf("%s did not upload", files[byID[id]].name)
	}
	for _, id := range done.Rejected {
		logf("%s was rejected for its size", files[byID[id]].name)
	}
	if n := len(done.Uploaded); n > 0 {
		logf("uploaded %d file(s) for %q", n, req.Check)
	}
	return result, nil
}

func prepare(req Request, logf Logf) ([]file, Result, error) {
	result := Result{JUnitComplete: !req.JUnitMissing}
	if len(req.JUnitPaths) > 0 && req.Since.IsZero() {
		// Without a start time, a report left by an earlier build can't be told
		// from this one's.
		result.JUnitComplete = false
	}
	var files []file
	if len(req.Log) > 0 {
		gz, err := gzipLog(req.Log, max(req.LogSize, int64(len(req.Log))))
		if err != nil {
			return nil, result, err
		}
		files = append(files, file{typ: api.FileTypeLog, name: "log", gz: gz})
	}
	paths := req.JUnitPaths
	if len(paths) > MaxJUnitFiles {
		logf("found %d JUnit reports, uploading the first %d", len(paths), MaxJUnitFiles)
		paths = paths[:MaxJUnitFiles]
		result.JUnitComplete = false
	}
	seen := map[junit.Failure]bool{}
	for _, path := range paths {
		info, err := os.Stat(path)
		if err == nil && !req.Since.IsZero() && info.ModTime().Before(req.Since) {
			logf("skipping %s: written before this run started", path)
			result.JUnitComplete = false
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			logf("skipping %s: %v", path, err)
			result.JUnitComplete = false
			continue
		}
		if len(data) == 0 {
			result.JUnitComplete = false
			continue
		}
		report, err := junit.Parse(data)
		if err != nil {
			logf("could not read %s as JUnit: %v", path, err)
			result.JUnitComplete = false
		}
		for _, f := range report.Failures {
			switch {
			case seen[f]:
			case utf8.RuneCountInString(f.Classname) > MaxTestNameChars ||
				utf8.RuneCountInString(f.Name) > MaxTestNameChars:
				logf("not sending a failed test with a name over %d characters", MaxTestNameChars)
				result.JUnitComplete = false
			case len(result.Failures) >= MaxFailedTests:
				result.JUnitComplete = false
			default:
				seen[f] = true
				result.Failures = append(result.Failures, f)
			}
		}
		gz, err := gzipBytes(data)
		if err != nil {
			return nil, result, err
		}
		if len(gz) > MaxFileBytes {
			logf("skipping %s: over %d MiB compressed", path, MaxFileBytes>>20)
			result.JUnitComplete = false
			continue
		}
		files = append(files, file{typ: api.FileTypeJUnit, name: path, gz: gz})
	}
	return files, result, nil
}

// gzipLog keeps as much of the log's tail as fits under the compressed cap.
// text is the end of a log of total bytes.
func gzipLog(text []byte, total int64) ([]byte, error) {
	for n := LogTailBytes; ; n /= 2 {
		gz, err := gzipBytes(tail(text, n, total))
		if err != nil || len(gz) <= MaxFileBytes {
			return gz, err
		}
	}
}

func gzipBytes(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
