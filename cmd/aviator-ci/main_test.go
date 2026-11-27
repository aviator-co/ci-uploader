package main

import "testing"

func TestUploadKeepsExitCodeOnBadFlags(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want int
	}{
		{[]string{"upload", "--check", "x", "--exit-code", "0", "--newer-flag"}, 0},
		{[]string{"upload", "--newer-flag", "--exit-code=3"}, 3},
		{[]string{"upload", "-exit-code", "0", "stray"}, 0},
		{[]string{"upload", "--exit-code", "", "--check", "x"}, 2},
		{[]string{"upload", "--newer-flag"}, 2},
		{[]string{"upload", "--exit-code", "0", "--check", "x"}, 0},
		{[]string{"upload", "--check", "x", "--log", "out.txt"}, 2},
		{[]string{"upload", "--newer-flag", "--exit-code=-1"}, 1},
		{[]string{"upload", "--exit-code", "-3"}, 1},
		{[]string{"upload", "--check", "x", "--exit-code", "0", "--junit", "r.xml"}, 2},
		{[]string{"upload", "--exit-code", "3", "--quarantine"}, 3},
		{[]string{"run", "--check", "x", "--quarantine", "--", "true"}, 2},
		{[]string{"run", "--check", "x", "--junit", "r.xml", "--quarantine", "--quarantine-exit-codes", "x", "--", "true"}, 2},
	} {
		if got := run(tc.args); got != tc.want {
			t.Errorf("run(%q) = %d, want %d", tc.args, got, tc.want)
		}
	}
}

func TestParseStartedAt(t *testing.T) {
	for in, want := range map[string]int64{"1700000000": 1700000000, "2023-11-14T22:13:20Z": 1700000000} {
		got, err := parseStartedAt(in)
		if err != nil || got.Unix() != want {
			t.Errorf("parseStartedAt(%q) = %v, %v", in, got, err)
		}
	}
	if got, err := parseStartedAt(""); err != nil || !got.IsZero() {
		t.Errorf("empty = %v, %v", got, err)
	}
	for _, in := range []string{"yesterday", "1700000000000"} {
		if _, err := parseStartedAt(in); err == nil {
			t.Errorf("parseStartedAt(%q): no error", in)
		}
	}
}
