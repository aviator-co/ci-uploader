// Package quarantine decides whether a failed build's failures are all
// quarantined, so the build can pass.
package quarantine

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/aviator-co/ci-uploader/internal/api"
	"github.com/aviator-co/ci-uploader/internal/junit"
)

// DefaultExitCodes are the codes test runners such as pytest and Jest use for
// "tests failed". Build tools (Gradle, Maven, make) use the same codes for any
// failure, so they can't tell a compile error from a test failure.
var DefaultExitCodes = []int{1}

// Decision is whether to pass the build, and why.
type Decision struct {
	Pass   bool
	Reason string
}

// Decide passes the build only when Aviator quarantines every failure, the reports
// were complete, the exit code means "tests failed", and every failure is
// quarantined. Anything else keeps the real result.
func Decide(code int, exitCodes []int, complete bool, failures []junit.Failure, q *api.Quarantine) Decision {
	switch {
	case code == 0:
		return Decision{Reason: "the build passed"}
	case q == nil:
		return Decision{Reason: "Aviator did not answer with a quarantine decision"}
	case len(failures) == 0:
		return Decision{Reason: "the JUnit reports show no failed test, so something else failed"}
	case !complete:
		return Decision{Reason: "a JUnit report was missing, stale or unreadable"}
	case !slices.Contains(exitCodes, code):
		return Decision{Reason: fmt.Sprintf("exit code %d is not a test failure (--quarantine-exit-codes)", code)}
	}
	names := make([]string, len(failures))
	for i, f := range failures {
		names[i] = f.Classname + "::" + f.Name
	}
	loose := notIn(failures, q.Tests)
	if len(loose) == 0 {
		return Decision{Pass: true, Reason: fmt.Sprintf("every failure is quarantined: %s", summarize(names))}
	}
	if len(q.ShadowTests) > 0 && len(notIn(failures, q.ShadowTests)) == 0 {
		return Decision{Reason: fmt.Sprintf("shadow mode: would have passed, every failure is quarantined: %s", summarize(names))}
	}
	return Decision{Reason: fmt.Sprintf("%d failed test(s) are not quarantined: %s", len(loose), summarize(loose))}
}

// notIn names the failures whose test isn't in tests.
func notIn(failures []junit.Failure, tests []api.TestRef) []string {
	in := map[api.TestRef]bool{}
	for _, t := range tests {
		in[t] = true
	}
	var out []string
	for _, f := range failures {
		if !in[api.TestRef{Classname: f.Classname, Name: f.Name}] {
			out = append(out, f.Classname+"::"+f.Name)
		}
	}
	return out
}

func summarize(names []string) string {
	if len(names) <= 5 {
		return strings.Join(names, ", ")
	}
	return strings.Join(names[:5], ", ") + " and " + strconv.Itoa(len(names)-5) + " more"
}

// ParseExitCodes reads a comma-separated list such as "1,2".
func ParseExitCodes(s string) ([]int, error) {
	var codes []int
	for _, part := range strings.Split(s, ",") {
		code, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || code <= 0 {
			return nil, fmt.Errorf("bad exit code %q", part)
		}
		codes = append(codes, code)
	}
	return codes, nil
}
