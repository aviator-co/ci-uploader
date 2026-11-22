// Package upload sends a failed check's log and test reports to Aviator.
package upload

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/aviator-co/ci-uploader/internal/api"
)

const (
	// Aviator's limits: each gzipped file, and a log once decompressed.
	MaxFileBytes    = 10 << 20
	MaxLogTextBytes = 40 << 20
	MaxJUnitFiles   = 100

	// The end of a log is where failures are, so a longer one keeps its tail.
	LogTailBytes = 16 << 20
)

type Request struct {
	Repo     string
	Commit   string
	Check    string
	BuildURL string
	// The end of the log, and the whole log's size if that's longer.
	Log        []byte
	LogSize    int64
	JUnitPaths []string
}

type Logf func(format string, args ...any)

type file struct {
	typ  api.FileType
	name string
	gz   []byte
}

// Run uploads the files in req. Nothing is uploaded when Aviator won't read
// them, which isn't an error.
func Run(ctx context.Context, client *api.Client, req Request, logf Logf) error {
	files, err := prepare(req, logf)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		logf("nothing to upload")
		return nil
	}

	declared := make([]api.DeclaredFile, len(files))
	for i, f := range files {
		declared[i] = api.DeclaredFile{Type: f.typ, SizeBytes: int64(len(f.gz))}
	}
	created, err := client.CreateUploads(ctx, api.CreateRequest{
		Repo:      req.Repo,
		CommitSHA: req.Commit,
		CheckName: req.Check,
		BuildURL:  req.BuildURL,
		Files:     declared,
	})
	if err != nil {
		return fmt.Errorf("declaring uploads: %w", err)
	}
	if created.Skipped != nil {
		logf("not uploading: %s", *created.Skipped)
		return nil
	}
	if len(created.Files) != len(files) {
		return fmt.Errorf("asked for %d upload URLs, got %d", len(files), len(created.Files))
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

	done, err := client.CompleteUploads(ctx, ids)
	if err != nil {
		return fmt.Errorf("confirming uploads: %w", err)
	}
	if len(done.Missing) > 0 {
		for _, id := range done.Missing {
			if err := put(byID[id]); err != nil {
				logf("could not upload %s: %v", files[byID[id]].name, err)
			}
		}
		if done, err = client.CompleteUploads(ctx, done.Missing); err != nil {
			return fmt.Errorf("confirming uploads: %w", err)
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
	return nil
}

func prepare(req Request, logf Logf) ([]file, error) {
	var files []file
	if len(req.Log) > 0 {
		gz, err := gzipLog(req.Log, max(req.LogSize, int64(len(req.Log))))
		if err != nil {
			return nil, err
		}
		files = append(files, file{typ: api.FileTypeLog, name: "log", gz: gz})
	}
	paths := req.JUnitPaths
	if len(paths) > MaxJUnitFiles {
		logf("found %d JUnit reports, uploading the first %d", len(paths), MaxJUnitFiles)
		paths = paths[:MaxJUnitFiles]
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			logf("skipping %s: %v", path, err)
			continue
		}
		if len(data) == 0 {
			continue
		}
		gz, err := gzipBytes(data)
		if err != nil {
			return nil, err
		}
		if len(gz) > MaxFileBytes {
			logf("skipping %s: over %d MiB compressed", path, MaxFileBytes>>20)
			continue
		}
		files = append(files, file{typ: api.FileTypeJUnit, name: path, gz: gz})
	}
	return files, nil
}

// gzipLog keeps as much of the log's tail as fits under the compressed cap.
// text is the end of a log of total bytes.
func gzipLog(text []byte, total int64) ([]byte, error) {
	for n := min(LogTailBytes, MaxLogTextBytes); ; n /= 2 {
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
