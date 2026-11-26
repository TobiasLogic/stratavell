package textproc

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

type LogDigest struct {
	Lines       int      `json:"lines"`
	Bytes       int      `json:"bytes"`
	Errors      []Line   `json:"errors,omitempty"`
	Warnings    int      `json:"warnings"`
	FailedTests []string `json:"failed_tests,omitempty"`
	PassedTests int      `json:"passed_tests"`
	Locations   []string `json:"locations,omitempty"`
	SummaryLine string   `json:"summary_line,omitempty"`
}

type Line struct {
	N    int    `json:"n"`
	Text string `json:"text"`
}

var (
	reError    = regexp.MustCompile(`(?i)(\berror\b|\bERR!|\bfatal\b|\bpanic:|\bexception\b|traceback \(most recent call last\)|\bFAIL\b|\bfailed\b|undefined reference|segmentation fault|\bassert(ion)?(error)?\b.*(fail|mismatch|expected))`)
	reWarn     = regexp.MustCompile(`(?i)\bwarn(ing)?\b`)
	reAssert   = regexp.MustCompile(`(?i)(\b(expected|want(ed)?|got|actual|mismatch|assert\w*)\b.*\b(got|want|but|actual|expected|to (be|equal))\b|[\w./-]+\.\w+:\d+(:\d+)?: .*\b(expected|mismatch|got|want|undefined|cannot|error)\b)`)
	reBareFail = regexp.MustCompile(`^(FAIL\b.*|--- FAIL: .*|FAILED.*|ok\s+\S+.*)$`)
	reFailTest = []*regexp.Regexp{
		regexp.MustCompile(`^--- FAIL: (\S+)`),
		regexp.MustCompile(`^FAILED (\S+::\S+)`),
		regexp.MustCompile(`^\s*✕ (.+?)(?: \(\d+ ?ms\))?$`),
		regexp.MustCompile(`^test (\S+) \.\.\. FAILED`),
		regexp.MustCompile(`^\s*FAIL\s+(\S+\.(?:test|spec)\.[jt]sx?)`),
	}
	rePassTest = []*regexp.Regexp{
		regexp.MustCompile(`^--- PASS: `),
		regexp.MustCompile(`^test \S+ \.\.\. ok`),
		regexp.MustCompile(` PASSED`),
		regexp.MustCompile(`^\s*✓ `),
	}
	reLocation = regexp.MustCompile(`([A-Za-z0-9_\-./]+\.(?:go|py|js|ts|tsx|jsx|rs|java|c|cc|cpp|h|hpp|rb|php|cs|kt|swift|scala|lua|sh|yaml|yml|json|toml)):(\d+)(?::(\d+))?`)
	reSummary  = regexp.MustCompile(`(?i)^(ok\s+\S+|FAIL\s+\S+|=+ .*(passed|failed|error).* =+|Tests?:\s+.*|test result: .*|\d+ (passed|failed).*|Ran \d+ tests? in .*|BUILD (SUCCESS|FAILED).*)`)
)

func DigestLog(s string, maxErrors int) LogDigest {
	d := LogDigest{Bytes: len(s)}
	var primary, secondary, bare []Line
	seenLoc := map[string]bool{}
	seenErr := map[string]bool{}
	n := 0
	for line := range strings.Lines(s) {
		n++
		t := strings.TrimRight(line, "\r\n")
		if t == "" {
			continue
		}
		lower := strings.ToLower(t)
		if strings.Contains(t, "FAIL") || strings.Contains(t, "✕") {
			for _, re := range reFailTest {
				if m := re.FindStringSubmatch(t); m != nil {
					d.FailedTests = appendUnique(d.FailedTests, m[1], 64)
					break
				}
			}
		}
		if strings.Contains(t, "PASS") || strings.Contains(t, "ok") || strings.Contains(t, "✓") {
			for _, re := range rePassTest {
				if re.MatchString(t) {
					d.PassedTests++
					break
				}
			}
		}
		if strings.Contains(t, ":") && strings.Contains(t, ".") && len(seenLoc) < 32 {
			for _, m := range reLocation.FindAllString(t, 4) {
				if !seenLoc[m] && len(seenLoc) < 32 {
					seenLoc[m] = true
					d.Locations = append(d.Locations, m)
				}
			}
		}
		if !hasSignal(lower) {
			if summaryHint(t) && reSummary.MatchString(strings.TrimSpace(t)) {
				d.SummaryLine = clip(strings.TrimSpace(t), 200)
			}
			continue
		}
		tt := strings.TrimSpace(t)
		if reAssert.MatchString(tt) {
			key := normalizeErr(t)
			if !seenErr[key] {
				seenErr[key] = true
				primary = append(primary, Line{N: n, Text: clip(tt, 300)})
			}
		} else if reError.MatchString(t) {
			key := normalizeErr(t)
			if !seenErr[key] {
				seenErr[key] = true
				if reBareFail.MatchString(tt) {
					bare = append(bare, Line{N: n, Text: clip(tt, 300)})
				} else {
					secondary = append(secondary, Line{N: n, Text: clip(tt, 300)})
				}
			}
		} else if reWarn.MatchString(t) {
			d.Warnings++
		}
		if summaryHint(t) && reSummary.MatchString(strings.TrimSpace(t)) {
			d.SummaryLine = clip(strings.TrimSpace(t), 200)
		}
	}
	d.Lines = n
	for _, group := range [][]Line{primary, secondary, bare} {
		for _, l := range group {
			if len(d.Errors) < maxErrors {
				d.Errors = append(d.Errors, l)
			}
		}
	}
	return d
}

var reDigits = regexp.MustCompile(`\d+`)

var signalWords = []string{"err", "fail", "panic", "exception", "fatal", "assert", "expected", "warn", "mismatch", "undefined", "cannot", "traceback", "segmentation", "want", "got ", "actual"}

func hasSignal(lower string) bool {
	for _, w := range signalWords {
		if strings.Contains(lower, w) {
			return true
		}
	}
	return false
}

func summaryHint(t string) bool {
	return strings.Contains(t, "ok") || strings.Contains(t, "FAIL") || strings.Contains(t, "passed") || strings.Contains(t, "failed") || strings.Contains(t, "Test") || strings.Contains(t, "test result") || strings.Contains(t, "Ran ") || strings.Contains(t, "BUILD") || strings.Contains(t, "error") || strings.Contains(t, "=====")
}

const scanCap = 64 << 10

func capText(s string) string {
	if len(s) <= scanCap {
		return s
	}
	cut := scanCap
	for cut > 0 && !isRuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

func normalizeErr(s string) string {
	return reDigits.ReplaceAllString(strings.TrimSpace(s), "#")
}

func appendUnique(xs []string, s string, max int) []string {
	if len(xs) >= max {
		return xs
	}
	for _, x := range xs {
		if x == s {
			return xs
		}
	}
	return append(xs, s)
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !isRuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "..."
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

func Clip(s string, n int) string { return clip(s, n) }

type Preview struct {
	Head      string
	Tail      string
	Omitted   int
	TotalLine int
}

func HeadTail(s string, headLines, tailLines, maxLineBytes int) Preview {
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	p := Preview{TotalLine: len(lines)}
	clipLines := func(ls []string) string {
		out := make([]string, len(ls))
		for i, l := range ls {
			out[i] = clip(l, maxLineBytes)
		}
		return strings.Join(out, "\n")
	}
	if len(lines) <= headLines+tailLines {
		p.Head = clipLines(lines)
		return p
	}
	p.Head = clipLines(lines[:headLines])
	p.Tail = clipLines(lines[len(lines)-tailLines:])
	p.Omitted = len(lines) - headLines - tailLines
	return p
}

var (
	reGoFunc   = regexp.MustCompile(`(?m)^func\s+(?:\([^)]*\)\s*)?([A-Za-z_][A-Za-z0-9_]*)`)
	reGoType   = regexp.MustCompile(`(?m)^type\s+([A-Za-z_][A-Za-z0-9_]*)`)
	rePyDef    = regexp.MustCompile(`(?m)^\s*(?:async\s+)?(?:def|class)\s+([A-Za-z_][A-Za-z0-9_]*)`)
	reJSFunc   = regexp.MustCompile(`(?m)(?:function\s+|class\s+|interface\s+|(?:const|let|var)\s+)([A-Za-z_$][A-Za-z0-9_$]*)`)
	reRustFn   = regexp.MustCompile(`(?m)^\s*(?:pub(?:\([^)]*\))?\s+)?(?:fn|struct|enum|trait|impl)\s+([A-Za-z_][A-Za-z0-9_]*)`)
	reJavaDecl = regexp.MustCompile(`(?m)^\s*(?:public|private|protected)\s+(?:static\s+)?(?:final\s+)?(?:class|interface|enum|[A-Za-z0-9_<>\[\]]+)\s+([A-Za-z_][A-Za-z0-9_]*)\s*[({]`)
	reIdent    = regexp.MustCompile(`\b[A-Za-z_][A-Za-z0-9_]{2,}\b`)
)

func Symbols(text string, max int) []string {
	text = capText(text)
	set := map[string]bool{}
	var out []string
	add := func(s string) {
		if len(out) >= max || set[s] || isStopIdent(s) {
			return
		}
		set[s] = true
		out = append(out, s)
	}
	for _, re := range []*regexp.Regexp{reGoFunc, reGoType, rePyDef, reJSFunc, reRustFn, reJavaDecl} {
		for _, m := range re.FindAllStringSubmatch(text, max) {
			add(m[1])
		}
	}
	for _, m := range reIdent.FindAllString(text, -1) {
		if len(out) >= max {
			break
		}
		if looksLikeCodeIdent(m) {
			add(m)
		}
	}
	return out
}

func looksLikeCodeIdent(s string) bool {
	hasUpperInside := false
	for i := 1; i < len(s); i++ {
		if s[i] >= 'A' && s[i] <= 'Z' {
			hasUpperInside = true
			break
		}
	}
	return strings.Contains(s, "_") || hasUpperInside
}

var stopIdents = map[string]bool{"the": true, "and": true, "for": true, "func": true, "return": true, "true": true, "false": true, "nil": true, "null": true, "None": true, "self": true, "this": true}

func isStopIdent(s string) bool { return stopIdents[s] }

func SplitIdent(s string) []string {
	var parts []string
	var cur []rune
	flush := func() {
		if len(cur) > 1 {
			parts = append(parts, strings.ToLower(string(cur)))
		}
		cur = cur[:0]
	}
	rs := []rune(s)
	for i, r := range rs {
		switch {
		case r == '_' || r == '-' || r == '.' || r == '/':
			flush()
		case unicode.IsUpper(r) && i > 0 && (unicode.IsLower(rs[i-1]) || (i+1 < len(rs) && unicode.IsLower(rs[i+1]) && unicode.IsUpper(rs[i-1]))):
			flush()
			cur = append(cur, r)
		default:
			cur = append(cur, r)
		}
	}
	flush()
	return parts
}

var rePath = regexp.MustCompile(`(?:^|[\s"'(\[=:])((?:\.{0,2}/)?(?:[A-Za-z0-9_\-.]+/)+[A-Za-z0-9_\-.]+\.[A-Za-z0-9]{1,8})\b`)

func Paths(text string, max int) []string {
	text = capText(text)
	set := map[string]bool{}
	var out []string
	for _, m := range rePath.FindAllStringSubmatch(text, -1) {
		p := strings.TrimPrefix(m[1], "./")
		if !set[p] {
			set[p] = true
			out = append(out, p)
			if len(out) >= max {
				break
			}
		}
	}
	return out
}

var reDirective = regexp.MustCompile(`(?i)\b(must|must not|mustn't|never|always|do not|don't|dont|required|requirement|only use|only|should not|shouldn't|avoid|ensure|make sure|forbidden|not allowed|no more than|at most|at least|keep)\b`)

func DirectiveSentences(text string, max int) []string {
	var out []string
	for _, s := range splitSentences(text) {
		s = strings.TrimSpace(s)
		if len(s) < 6 {
			continue
		}
		if reDirective.MatchString(s) {
			out = append(out, clip(s, 400))
			if len(out) >= max {
				break
			}
		}
	}
	return out
}

func splitSentences(text string) []string {
	var out []string
	for line := range strings.Lines(text) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		start := 0
		for i := 0; i < len(line); i++ {
			c := line[i]
			if (c == '.' || c == '!' || c == '?' || c == ';') && (i+1 == len(line) || line[i+1] == ' ') {
				out = append(out, line[start:i+1])
				start = i + 1
			}
		}
		if start < len(line) {
			out = append(out, line[start:])
		}
	}
	return out
}

var reTestCmd = regexp.MustCompile(`(?i)(\bgo test\b|\bpytest\b|\bnpm (run )?test\b|\byarn test\b|\bpnpm test\b|\bcargo test\b|\bmvn test\b|\bgradle\w* test\b|\bjest\b|\bvitest\b|\bmake (test|check)\b|\bctest\b|\bunittest\b|\brspec\b|\bphpunit\b)`)
var reBuildCmd = regexp.MustCompile(`(?i)(\bgo build\b|\bgo vet\b|\bcargo (build|check|clippy)\b|\bnpm run build\b|\btsc\b|\bmake\b|\bgcc\b|\bclang\b|\bmvn (compile|package)\b|\bgradle\w* build\b)`)

func IsTestCommand(cmd string) bool  { return reTestCmd.MatchString(cmd) }
func IsBuildCommand(cmd string) bool { return reBuildCmd.MatchString(cmd) }

func CommandKey(cmd string) string {
	f := strings.Fields(cmd)
	if len(f) > 6 {
		f = f[:6]
	}
	return strings.Join(f, " ")
}

func FormatDigest(d LogDigest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d lines, %d bytes", d.Lines, d.Bytes)
	if d.SummaryLine != "" {
		fmt.Fprintf(&b, "; last summary: %s", d.SummaryLine)
	}
	if len(d.FailedTests) > 0 {
		ft := append([]string(nil), d.FailedTests...)
		sort.Strings(ft)
		if len(ft) > 12 {
			ft = append(ft[:12], fmt.Sprintf("(+%d more)", len(d.FailedTests)-12))
		}
		fmt.Fprintf(&b, "\nfailed tests: %s", strings.Join(ft, ", "))
	}
	if d.PassedTests > 0 {
		fmt.Fprintf(&b, "\npassed tests: %d", d.PassedTests)
	}
	if len(d.Errors) > 0 {
		b.WriteString("\nerror lines:")
		for _, e := range d.Errors {
			fmt.Fprintf(&b, "\n  L%d: %s", e.N, e.Text)
		}
	}
	if d.Warnings > 0 {
		fmt.Fprintf(&b, "\nwarnings: %d", d.Warnings)
	}
	if len(d.Locations) > 0 {
		locs := d.Locations
		if len(locs) > 10 {
			locs = locs[:10]
		}
		fmt.Fprintf(&b, "\nlocations: %s", strings.Join(locs, ", "))
	}
	return b.String()
}
