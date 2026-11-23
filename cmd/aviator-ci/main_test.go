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
	} {
		if got := run(tc.args); got != tc.want {
			t.Errorf("run(%q) = %d, want %d", tc.args, got, tc.want)
		}
	}
}
