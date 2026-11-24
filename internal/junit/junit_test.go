package junit

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	report, err := Parse([]byte(`<?xml version="1.0"?>
<testsuites>
  <testsuite name="outer">
    <testsuite name="inner">
      <testcase classname="pkg.A" name="passes"/>
      <testcase classname="pkg.A" name="fails"><failure message="assert 1 == 2">
        Traceback...
      </failure></testcase>
      <testcase classname="pkg.A" name="skipped"><skipped/></testcase>
    </testsuite>
    <testcase name="errors"><error message="boom"/></testcase>
    <testcase classname="pkg.B" name="flaky"><flakyFailure message="first try"/></testcase>
  </testsuite>
</testsuites>`))
	if err != nil {
		t.Fatal(err)
	}
	want := []Failure{
		{Classname: "pkg.A", Name: "fails", Message: "assert 1 == 2", Trace: "Traceback..."},
		{Classname: "outer", Name: "errors", Message: "boom"},
	}
	if len(report.Failures) != len(want) {
		t.Fatalf("Failures = %+v", report.Failures)
	}
	for i := range want {
		if report.Failures[i] != want[i] {
			t.Errorf("Failures[%d] = %+v, want %+v", i, report.Failures[i], want[i])
		}
	}
}

func TestParseTruncatesLikeAviator(t *testing.T) {
	long := strings.Repeat("é", MaxMessageChars+10)
	report, err := Parse([]byte(`<testsuite name="s"><testcase classname="a" name="b"><failure message="` + long + `"/></testcase></testsuite>`))
	if err != nil {
		t.Fatal(err)
	}
	if got := []rune(report.Failures[0].Message); len(got) != MaxMessageChars {
		t.Errorf("message has %d characters", len(got))
	}
}

func TestParseRejectsTruncatedReports(t *testing.T) {
	if _, err := Parse([]byte(`<testsuite name="s"><testcase classname="a" name="b">`)); err == nil {
		t.Error("no error")
	}
}
