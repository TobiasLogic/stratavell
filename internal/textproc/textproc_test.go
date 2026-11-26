package textproc

import (
	"strings"
	"testing"
)

func TestDigestGoTest(t *testing.T) {
	log := "=== RUN   TestA\n--- PASS: TestA (0.00s)\n=== RUN   TestB\n--- FAIL: TestB (0.01s)\n    b_test.go:41: expected 3, got 4\npanic: runtime error: index out of range [recovered]\nFAIL\tgithub.com/x/y\t0.02s\n"
	d := DigestLog(log, 10)
	if len(d.FailedTests) != 1 || d.FailedTests[0] != "TestB" {
		t.Fatalf("failed tests %v", d.FailedTests)
	}
	if d.PassedTests != 1 || len(d.Errors) == 0 {
		t.Fatalf("digest %+v", d)
	}
	if len(d.Locations) == 0 || d.Locations[0] != "b_test.go:41" {
		t.Fatalf("locations %v", d.Locations)
	}
	if !strings.HasPrefix(d.SummaryLine, "FAIL") {
		t.Fatalf("summary %q", d.SummaryLine)
	}
}

func TestDigestPytest(t *testing.T) {
	log := "tests/test_x.py::test_ok PASSED\nFAILED tests/test_x.py::test_bad - AssertionError: assert 1 == 2\n===== 1 failed, 1 passed in 0.12s =====\n"
	d := DigestLog(log, 10)
	if len(d.FailedTests) != 1 || d.FailedTests[0] != "tests/test_x.py::test_bad" {
		t.Fatalf("%+v", d)
	}
}

func TestSplitIdent(t *testing.T) {
	got := strings.Join(SplitIdent("parseHTTPRequest_v2"), ",")
	if got != "parse,http,request,v2" {
		t.Fatalf("got %s", got)
	}
}

func TestDirectives(t *testing.T) {
	ds := DirectiveSentences("Please refactor the parser. Never touch vendor/. Ok thanks", 5)
	if len(ds) != 1 || !strings.Contains(ds[0], "Never touch vendor") {
		t.Fatalf("%v", ds)
	}
}

func TestPathsAndSymbols(t *testing.T) {
	p := Paths("edited ./pkg/a/b.go and src/x.ts:12", 5)
	if len(p) != 2 || p[0] != "pkg/a/b.go" {
		t.Fatalf("%v", p)
	}
	s := Symbols("func (s *Server) HandleRequest(ctx) {}\ntype QueryPlan struct{}", 10)
	if len(s) < 2 || s[0] != "HandleRequest" {
		t.Fatalf("%v", s)
	}
}

func FuzzDigest(f *testing.F) {
	f.Add("--- FAIL: TestX\nerror: boom at a.go:1:2\n")
	f.Fuzz(func(t *testing.T, s string) {
		d := DigestLog(s, 5)
		if len(d.Errors) > 5 {
			t.Fatal("error cap exceeded")
		}
		HeadTail(s, 3, 3, 40)
		Symbols(s, 10)
		Paths(s, 10)
		DirectiveSentences(s, 3)
		SplitIdent(s)
	})
}
