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
	"strings"
	"time"
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
}

type Slot struct {
	ID        int64    `json:"id"`
	Type      FileType `json:"type"`
	UploadURL string   `json:"upload_url"`
}

type CreateResponse struct {
	// Set when Aviator won't read the files, and Files is then empty.
	Skipped     *string `json:"skipped"`
	ContentType string  `json:"content_type"`
	Files       []Slot  `json:"files"`
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
		HTTP:      &http.Client{Timeout: 60 * time.Second},
		Backoff:   []time.Duration{time.Second, 3 * time.Second},
	}
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

// Put uploads body to a signed URL from CreateUploads.
func (c *Client) Put(ctx context.Context, url, contentType string, body []byte) error {
	return c.retry(ctx, func() error {
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", contentType)
		_, err = c.send(req)
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
		if err = attempt(); err == nil || !retriable(err) || i >= len(c.Backoff) {
			return err
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(c.Backoff[i]):
		}
	}
}

func retriable(err error) bool {
	var apiErr *Error
	if errors.As(err, &apiErr) {
		return apiErr.Status == http.StatusTooManyRequests || apiErr.Status >= 500
	}
	return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}
