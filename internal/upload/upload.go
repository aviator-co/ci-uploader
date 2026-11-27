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

	"github.com/aviator-co/ci-uploader/internal/api"
	"github.com/aviator-co/ci-uploader/internal/junit"
)

const (
	// Aviator's limits on each gzipped file and on the number of reports.
	MaxFileBytes  = 10 << 20
	MaxJUnitFiles = 100
	// Aviator's limit on the failed tests sent with an upload. A run that goes
	// past it can't be quarantined, so it's marked incomplete.
	MaxFailedTests = 1000

	// The end of a log is where failures are, so a longer one keeps its tail.
	// Well under Aviator's 40 MiB cap on a decompressed log.
	LogTailBytes = 16 << 20

	// Confirming a failed build's upload gets its own budget, so slow report
	// uploads that use up the caller's can't leave an uploaded log unconfirmed
	// and never read. A passing build's confirm stays inside the caller's.
	confirmTimeout = 20 * time.Second
)

type Request struct {
	Repo     string
	Commit   string
	Check    string
	BuildURL string
	Branch   string
	// The pull request a PR build is for, or 0.
	PullRequest int
	Variant     string
	ExitCode    int
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
	// When the test command finished.
	Finished time.Time
}

// Result is what the reports said and what Aviator answered.
type Result struct {
	// Each distinct failure, up to MaxFailedTests.
	Failures []junit.Failure
	// Every report was found, parsed, and written after Since, and every
	// failure fits what Aviator accepts.
	JUnitComplete bool
	// Distinct test cases across the reports.
	TestCount  int
	Quarantine *api.Quarantine
	// Every report Aviator wanted was uploaded and confirmed.
	ReportsConfirmed bool
	TestRunID        *int64
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
		Repo:              req.Repo,
		CommitSHA:         req.Commit,
		CheckName:         req.Check,
		BuildURL:          req.BuildURL,
		Files:             declared,
		Branch:            req.Branch,
		PullRequestNumber: req.PullRequest,
		Variant:           req.Variant,
		ExitCode:          &req.ExitCode,
		FailedTests:       result.Failures,
	}
	if len(req.JUnitPaths) > 0 {
		create.JUnitComplete = &result.JUnitComplete
		create.TestCount = &result.TestCount
	}
	if !req.Since.IsZero() && !req.Finished.IsZero() {
		create.StartedAt, create.FinishedAt = &req.Since, &req.Finished
	}
	created, err := client.CreateUploads(ctx, create)
	if err != nil {
		return result, fmt.Errorf("declaring uploads: %w", err)
	}
	result.Quarantine = created.Quarantine
	result.TestRunID = created.TestRunID
	if created.Skipped != nil {
		logf("not uploading: %s", *created.Skipped)
		return result, nil
	}
	slots, err := wanted(created.Files, len(files))
	if err != nil {
		return result, err
	}
	if len(slots) == 0 {
		logf("not uploading: Aviator wants none of the files")
		return result, nil
	}

	byID := make(map[int64]slot, len(slots))
	ids := make([]int64, len(slots))
	for i, s := range slots {
		byID[s.ID] = s
		ids[i] = s.ID
	}
	put := func(s slot) error {
		err := client.Put(ctx, s.UploadURL, created.ContentType, files[s.file].gz)
		// Storage is write-once, so a retried PUT whose first attempt landed is
		// refused; /complete tells us whether it's there.
		var apiErr *api.Error
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusForbidden {
			return nil
		}
		return err
	}
	for _, s := range slots {
		if err := put(s); err != nil {
			logf("could not upload %s: %v", files[s.file].name, err)
		}
	}

	confirm := func(ids []int64) (*api.CompleteResponse, error) {
		if req.ExitCode == 0 {
			return client.CompleteUploads(ctx, ids)
		}
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
				logf("could not upload %s: %v", files[byID[id].file].name, err)
			}
		}
		if done, err = confirm(done.Missing); err != nil {
			return result, fmt.Errorf("confirming uploads: %w", err)
		}
	}
	for _, id := range done.Missing {
		logf("%s did not upload", files[byID[id].file].name)
	}
	for _, id := range done.Rejected {
		logf("%s was rejected for its size", files[byID[id].file].name)
	}
	uploaded := map[int64]bool{}
	for _, id := range done.Uploaded {
		uploaded[id] = true
	}
	result.ReportsConfirmed = true
	for _, s := range slots {
		if files[s.file].typ == api.FileTypeJUnit && !uploaded[s.ID] {
			result.ReportsConfirmed = false
		}
	}
	if n := len(done.Uploaded); n > 0 {
		logf("uploaded %d file(s) for %q", n, req.Check)
	}
	return result, nil
}

// slot is an upload URL and the file it's for.
type slot struct {
	api.Slot
	file int
}

// wanted pairs each URL with its file. An older Aviator sends no index and a
// URL for every file, in order.
func wanted(slots []api.Slot, files int) ([]slot, error) {
	out := make([]slot, len(slots))
	for i, s := range slots {
		file := i
		if s.Index != nil {
			file = *s.Index
		} else if len(slots) != files {
			return nil, fmt.Errorf("asked for %d upload URLs, got %d", files, len(slots))
		}
		if file < 0 || file >= files {
			return nil, fmt.Errorf("upload URL for file %d of %d", file, files)
		}
		out[i] = slot{Slot: s, file: file}
	}
	return out, nil
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
	cases := map[junit.Case]bool{}
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
		for _, c := range report.Cases {
			cases[c] = true
		}
		for _, f := range report.Failures {
			switch {
			case seen[f]:
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
	result.TestCount = len(cases)
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
