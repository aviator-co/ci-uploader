// Package api calls Aviator's CI upload API.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aviator-co/ci-uploader/internal/junit"
)

const DefaultHost = "https://api.aviator.co"

type FileType string

const (
	FileTypeLog   FileType = "log"
	FileTypeJUnit FileType = "junit"
)

type DeclaredFile struct {
	Type      FileType `json:"type"`
	SizeBytes int64    `json:"size_bytes"`
}

type CreateRequest struct {
	Repo      string         `json:"repo"`
	CommitSHA string         `json:"commit_sha"`
	CheckName string         `json:"check_name"`
	BuildURL  string         `json:"build_url,omitempty"`
	Files     []DeclaredFile `json:"files"`
	Branch    string         `json:"branch,omitempty"`
	// Set for a PR build, which Aviator never counts as the default branch.
	PullRequestNumber int    `json:"pull_request_number,omitempty"`
	Variant           string `json:"variant,omitempty"`
	ExitCode          *int   `json:"exit_code,omitempty"`
	// When the test command started and finished, so Aviator pairs a retry
	// only with a run that finished before it started.
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	// Whether every report parsed and was written by this run.
	JUnitComplete *bool `json:"junit_complete,omitempty"`
	// Distinct test cases across the reports, so Aviator can refuse to
	// quarantine a run that ran fewer tests than usual.
	TestCount   *int            `json:"test_count,omitempty"`
	FailedTests []junit.Failure `json:"failed_tests,omitempty"`
}

type Slot struct {
	// The declared file's position. Aviator only hands out URLs for the files
	// it will read.
	Index     *int     `json:"index"`
	ID        int64    `json:"id"`
	Type      FileType `json:"type"`
	UploadURL string   `json:"upload_url"`
}

type CreateResponse struct {
	// Set when Aviator won't read the files, and Files is then empty.
	Skipped     *string `json:"skipped"`
	ContentType string  `json:"content_type"`
	Files       []Slot  `json:"files"`
	// Set when Aviator tracks this repository's tests.
	Quarantine *Quarantine `json:"quarantine"`
	TestRunID  *int64      `json:"test_run_id"`
}

// Quarantine says which of the failed tests are quarantined.
type Quarantine struct {
	// Tests whose failures may pass the build.
	Tests []TestRef `json:"tests"`
	// In shadow mode, the tests that would have been quarantined. Tests is
	// then empty.
	ShadowTests []TestRef `json:"shadow_tests"`
}

type TestRef struct {
	Classname string `json:"classname"`
	Name      string `json:"name"`
}

type CompleteResponse struct {
	Uploaded []int64 `json:"uploaded"`
	Missing  []int64 `json:"missing"`
	Rejected []int64 `json:"rejected"`
	Unknown  []int64 `json:"unknown"`
}

// Error is a non-2xx response.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("HTTP %d: %s", e.Status, e.Message)
	}
	return fmt.Sprintf("HTTP %d", e.Status)
}

type Client struct {
	Host      string
	Token     string
	UserAgent string
	HTTP      *http.Client
	// Waits between attempts of a request that failed in a way worth retrying.
	Backoff []time.Duration
}

func New(host, token, userAgent string) *Client {
	if host == "" {
		host = DefaultHost
	}
	return &Client{
		Host:      strings.TrimRight(host, "/"),
		Token:     token,
		UserAgent: userAgent,
		HTTP:      newHTTPClient(),
		Backoff:   []time.Duration{time.Second, 3 * time.Second},
	}
}

// newHTTPClient has no overall timeout, since that would also cap sending the
// body and a large log on a slow uplink would then fail on every attempt. The
// wait for an answer once the body is sent is capped instead, and the caller's
// context bounds the rest.
func newHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 60 * time.Second
	return &http.Client{Transport: transport}
}

func (c *Client) CreateUploads(ctx context.Context, req CreateRequest) (*CreateResponse, error) {
	var out CreateResponse
	if err := c.postJSON(ctx, "/api/v1/ci/uploads", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) CompleteUploads(ctx context.Context, ids []int64) (*CompleteResponse, error) {
	var out CompleteResponse
	if err := c.postJSON(ctx, "/api/v1/ci/uploads/complete", map[string]any{"ids": ids}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// QuarantineApplied tells Aviator the build passed on its quarantine answer,
// so a later failure of the check is put down to another step.
func (c *Client) QuarantineApplied(ctx context.Context, testRunID int64) error {
	var out map[string]any
	return c.postJSON(ctx, fmt.Sprintf("/api/v1/ci/uploads/runs/%d/quarantine", testRunID), map[string]bool{"applied": true}, &out)
}

// Put uploads body to a signed URL from CreateUploads.
func (c *Client) Put(ctx context.Context, uploadURL, contentType string, body []byte) error {
	return c.retry(ctx, func() error {
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadURL, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", contentType)
		_, err = c.send(req)
		// A url.Error carries the signed URL, which lets anyone holding it
		// write there until it expires.
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			return fmt.Errorf("PUT failed: %w", urlErr.Err)
		}
		return err
	})
}

func (c *Client) postJSON(ctx context.Context, path string, body, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	return c.retry(ctx, func() error {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Host+path, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.Token)
		data, err := c.send(req)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("unexpected response from %s: %w", path, err)
		}
		return nil
	})
}

func (c *Client) send(req *http.Request) ([]byte, error) {
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		apiErr := &Error{Status: resp.StatusCode}
		var body struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		if json.Unmarshal(data, &body) == nil {
			apiErr.Code, apiErr.Message = body.Error, body.Message
		}
		return nil, apiErr
	}
	return data, nil
}

func (c *Client) retry(ctx context.Context, attempt func() error) error {
	var err error
	for i := 0; ; i++ {
		if err = attempt(); err == nil || !retriable(ctx, err) || i >= len(c.Backoff) {
			return err
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(c.Backoff[i]):
		}
	}
}

func retriable(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	var apiErr *Error
	if errors.As(err, &apiErr) {
		return apiErr.Status == http.StatusTooManyRequests || apiErr.Status >= 500
	}
	// A transport error, a response header timeout included.
	return true
}
