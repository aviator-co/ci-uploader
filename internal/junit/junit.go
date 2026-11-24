// Package junit reads the failed test cases from JUnit XML reports.
package junit

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"strings"
)

// Aviator truncates a failure's message and trace to these many characters
// when it ingests the report. Failures sent with an upload are truncated the
// same way, so both sides compute the same failure signature.
const (
	MaxMessageChars = 1024
	MaxTraceChars   = 4096
)

// Failure is a test case that failed or errored.
type Failure struct {
	Classname string `json:"classname"`
	Name      string `json:"name"`
	Message   string `json:"message,omitempty"`
	Trace     string `json:"trace,omitempty"`
}

// Report is what one report says.
type Report struct {
	Failures []Failure
}

// Parse reads every <testcase>, in nested suites too. A case without a
// classname takes its suite's name, as Aviator does.
func Parse(data []byte) (Report, error) {
	var report Report
	dec := xml.NewDecoder(bytes.NewReader(data))
	var suites []string
	root := false
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return report, err
		}
		switch el := tok.(type) {
		case xml.StartElement:
			root = true
			switch el.Name.Local {
			case "testsuite":
				suites = append(suites, attr(el, "name"))
			case "testcase":
				var tc testcase
				if err := dec.DecodeElement(&tc, &el); err != nil {
					return report, err
				}
				suite := ""
				if len(suites) > 0 {
					suite = suites[len(suites)-1]
				}
				if f, failed := tc.failure(suite); failed {
					report.Failures = append(report.Failures, f)
				}
			}
		case xml.EndElement:
			if el.Name.Local == "testsuite" && len(suites) > 0 {
				suites = suites[:len(suites)-1]
			}
		}
	}
	if !root {
		return report, errors.New("no XML element")
	}
	if len(suites) > 0 {
		return report, errors.New("report ends inside a testsuite")
	}
	return report, nil
}

type problem struct {
	Message string `xml:"message,attr"`
	Text    string `xml:",chardata"`
}

type testcase struct {
	Classname string    `xml:"classname,attr"`
	Name      string    `xml:"name,attr"`
	Failure   []problem `xml:"failure"`
	Error     []problem `xml:"error"`
}

func (tc testcase) failure(suite string) (Failure, bool) {
	var p problem
	switch {
	case len(tc.Failure) > 0:
		p = tc.Failure[0]
	case len(tc.Error) > 0:
		p = tc.Error[0]
	default:
		return Failure{}, false
	}
	classname := tc.Classname
	if classname == "" {
		classname = suite
	}
	return Failure{
		Classname: classname,
		Name:      tc.Name,
		Message:   truncate(p.Message, MaxMessageChars),
		Trace:     truncate(strings.TrimSpace(p.Text), MaxTraceChars),
	}, true
}

func attr(el xml.StartElement, name string) string {
	for _, a := range el.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

func truncate(s string, chars int) string {
	r := []rune(s)
	if len(r) <= chars {
		return s
	}
	return string(r[:chars])
}
