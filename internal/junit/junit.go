// Package junit reads the failed test cases from JUnit XML reports.
package junit

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"strings"
)

// Aviator truncates a test's names, and a failure's message and trace, to
// these many characters when it ingests the report. Failures sent with an
// upload are truncated the same way, so both sides compute the same failure
// signature.
const (
	MaxNameChars    = 4096
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

// Case names a test case.
type Case struct {
	Classname string
	Name      string
}

// Report is what one report says.
type Report struct {
	// Every test case, failed or not.
	Cases    []Case
	Failures []Failure
}

// Parse reads every <testcase>, in nested suites too. A case without a
// classname takes its suite's name, as Aviator does.
func Parse(data []byte) (Report, error) {
	var report Report
	dec := xml.NewDecoder(bytes.NewReader(normalizeAttrs(data)))
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
				report.Cases = append(report.Cases, tc.key(suite))
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
	k := tc.key(suite)
	return Failure{
		Classname: k.Classname,
		Name:      k.Name,
		Message:   truncate(p.Message, MaxMessageChars),
		Trace:     truncate(strings.TrimSpace(p.Text), MaxTraceChars),
	}, true
}

func (tc testcase) key(suite string) Case {
	classname := tc.Classname
	if classname == "" {
		classname = suite
	}
	return Case{Classname: truncate(classname, MaxNameChars), Name: truncate(tc.Name, MaxNameChars)}
}

// normalizeAttrs turns literal newlines and tabs inside start tags into
// spaces. XML parsers do that to attribute values, Aviator's included, but Go
// doesn't. Whitespace between attributes is insignificant, and an escaped
// "&#10;" stays a newline in both.
func normalizeAttrs(data []byte) []byte {
	dec := xml.NewDecoder(bytes.NewReader(data))
	out := make([]byte, 0, len(data))
	var last int64
	for {
		start := dec.InputOffset()
		tok, err := dec.RawToken()
		if err != nil {
			break
		}
		if _, ok := tok.(xml.StartElement); ok {
			end := dec.InputOffset()
			out = append(out, data[last:start]...)
			out = append(out, tagWhitespace.Replace(string(data[start:end]))...)
			last = end
		}
	}
	return append(out, data[last:]...)
}

var tagWhitespace = strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ", "\t", " ")

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
