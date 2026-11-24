package quarantine

import (
	"testing"

	"github.com/aviator-co/ci-uploader/internal/api"
	"github.com/aviator-co/ci-uploader/internal/junit"
)

func TestDecide(t *testing.T) {
	flaky := junit.Failure{Classname: "a", Name: "flaky"}
	broken := junit.Failure{Classname: "a", Name: "real"}
	enforce := &api.Quarantine{Mode: "enforce", Tests: []api.TestRef{{Classname: "a", Name: "flaky"}}}
	shadow := &api.Quarantine{Mode: "shadow", Tests: enforce.Tests}
	for _, tc := range []struct {
		name     string
		code     int
		complete bool
		failures []junit.Failure
		q        *api.Quarantine
		pass     bool
	}{
		{"all quarantined", 1, true, []junit.Failure{flaky}, enforce, true},
		{"one real failure", 1, true, []junit.Failure{flaky, broken}, enforce, false},
		{"shadow mode", 1, true, []junit.Failure{flaky}, shadow, false},
		{"no answer", 1, true, []junit.Failure{flaky}, nil, false},
		{"no failed tests", 1, true, nil, enforce, false},
		{"incomplete reports", 1, false, []junit.Failure{flaky}, enforce, false},
		{"crash exit code", 137, true, []junit.Failure{flaky}, enforce, false},
	} {
		d := Decide(tc.code, DefaultExitCodes, tc.complete, tc.failures, tc.q)
		if d.Pass != tc.pass {
			t.Errorf("%s: pass = %v (%s)", tc.name, d.Pass, d.Reason)
		}
	}
}

func TestParseExitCodes(t *testing.T) {
	if codes, err := ParseExitCodes("1, 2"); err != nil || len(codes) != 2 || codes[1] != 2 {
		t.Errorf("codes = %v, %v", codes, err)
	}
	if _, err := ParseExitCodes("1,x"); err == nil {
		t.Error("no error")
	}
}
