package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRetriesServerErrorsOnly(t *testing.T) {
	for _, tc := range []struct {
		statuses []int
		calls    int
		wantErr  int
	}{
		{statuses: []int{503, 200}, calls: 2},
		{statuses: []int{500, 502, 504}, calls: 3, wantErr: 504},
		{statuses: []int{403}, calls: 1, wantErr: 403},
	} {
		calls := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			status := tc.statuses[calls]
			calls++
			w.WriteHeader(status)
			if status == 200 {
				_, _ = w.Write([]byte(`{"uploaded":[1]}`))
			} else {
				_, _ = w.Write([]byte(`{"error":"x","message":"nope"}`))
			}
		}))
		c := New(srv.URL, "tok", "test")
		c.Backoff = []time.Duration{0, 0}
		_, err := c.CompleteUploads(t.Context(), []int64{1})
		srv.Close()

		if calls != tc.calls {
			t.Errorf("%v: %d calls, want %d", tc.statuses, calls, tc.calls)
		}
		var apiErr *Error
		switch {
		case tc.wantErr == 0 && err != nil:
			t.Errorf("%v: %v", tc.statuses, err)
		case tc.wantErr != 0 && (!errors.As(err, &apiErr) || apiErr.Status != tc.wantErr || apiErr.Message != "nope"):
			t.Errorf("%v: err = %v", tc.statuses, err)
		}
	}
}
