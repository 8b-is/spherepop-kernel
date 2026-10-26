package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func do(t *testing.T, stdin string, args ...string) (code int, out, errOut string) {
	t.Helper()
	var o, e bytes.Buffer
	code = run(args, strings.NewReader(stdin), &o, &e)
	return code, o.String(), e.String()
}

func TestReduceWithTrailingFlags(t *testing.T) {
	code, out, _ := do(t, "", "reduce", "S K K x", "--fuel", "100", "--trace")
	if code != exitOK {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{"   0", "   1  S", "   2  K", "normal form in 2 steps (lo)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "\nx\n") {
		t.Errorf("result line missing:\n%s", out)
	}
}

func TestReduceExhaustedExitCode(t *testing.T) {
	code, out, _ := do(t, "", "reduce", "--fuel", "10", "--def", "w=S (S K K) (S K K)", "w w")
	if code != exitExhausted || !strings.Contains(out, "fuel exhausted after 10 steps") {
		t.Fatalf("exit %d, out %q", code, out)
	}
}

func TestRecordThenCheck(t *testing.T) {
	file := filepath.Join(t.TempDir(), "trace.json")
	if code, _, e := do(t, "", "reduce", "S K K x", "--out", file); code != exitOK {
		t.Fatalf("reduce exit %d: %s", code, e)
	}
	code, out, _ := do(t, "", "check", file, "--strategy")
	if code != exitOK || !strings.HasPrefix(out, "valid: 2 steps, final x (normal form)") {
		t.Fatalf("exit %d, out %q", code, out)
	}
	// Standard input works too.
	b, _ := os.ReadFile(file)
	if code, _, _ := do(t, string(b), "check", "-"); code != exitOK {
		t.Fatalf("stdin check exit %d", code)
	}
	// Tampering is reported and fails.
	bad := strings.Replace(string(b), `"rule": "K"`, `"rule": "I"`, 1)
	code, out, _ = do(t, bad, "check", "-")
	if code != exitFailure || !strings.HasPrefix(out, "invalid:") {
		t.Fatalf("tampered trace: exit %d, out %q", code, out)
	}
}

func TestScopedReduceFlags(t *testing.T) {
	code, out, _ := do(t, "", "reduce", "<K a b>_x", "--perm", "x=C")
	if code != exitOK || !strings.Contains(out, "<K a b>_x\nnormal form in 0 steps") {
		t.Fatalf("K should be stuck: exit %d, out %q", code, out)
	}
	code, out, _ = do(t, "", "reduce", "<K a b>_x", "--perm", "x=K")
	if code != exitOK || !strings.Contains(out, "<a>_x") {
		t.Fatalf("K should fire: exit %d, out %q", code, out)
	}
	// An unlabelled scope fails closed instead of guessing a permission.
	if code, _, e := do(t, "", "reduce", "<K a b>_x"); code != exitFailure || !strings.Contains(e, "label") {
		t.Fatalf("unknown label: exit %d, err %q", code, e)
	}
}

func TestUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"nope"},
		{"reduce"},
		{"reduce", "a", "b"},
		{"reduce", "S K K", "--strategy", "zz"},
		{"reduce", "S K K", "--perm", "nolist"},
		{"reduce", "S K K", "--perm", "x=Q"},
		{"reduce", "S K K", "--absorb", "maybe"},
		{"reduce", "S K K", "--def", "bad"},
		{"reduce", "((", "--fuel", "1"},
		{"check"},
		{"test", "extra"},
	} {
		if code, _, _ := do(t, "", args...); code != exitUsage {
			t.Errorf("%v: exit %d, want %d", args, code, exitUsage)
		}
	}
	if code, _, _ := do(t, "", "check", filepath.Join(t.TempDir(), "missing.json")); code != exitFailure {
		t.Error("missing file should fail")
	}
}

func TestTestAndRulesCommands(t *testing.T) {
	code, out, _ := do(t, "", "test")
	if code != exitOK || !strings.Contains(out, "0 failed") || strings.Contains(out, "FAIL") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	code, out, _ = do(t, "", "rules")
	if code != exitOK || !strings.Contains(out, "Abs") || !strings.Contains(out, "S f g x -> f x (g x)") {
		t.Fatalf("rules: exit %d\n%s", code, out)
	}
}

func TestSimAndCheckLog(t *testing.T) {
	file := filepath.Join(t.TempDir(), "log.json")
	code, out, e := do(t, "", "sim", "--entity", "loop=S (S K K) (S K K) (S (S K K) (S K K))",
		"--entity", "id=S K K x", "--budget", "3", "--ticks", "2", "--out", file)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, e)
	}
	for _, want := range []string{"tick 0   id           run  steps=2", "tick 1   id           skip", "loop         continues", "id           normal    x"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	code, out, _ = do(t, "", "checklog", file)
	if code != exitOK || !strings.HasPrefix(out, "valid: 4 scheduler events over 2 entities") {
		t.Fatalf("exit %d, out %q", code, out)
	}
	b, _ := os.ReadFile(file)
	bad := strings.Replace(string(b), `"budget": 3`, `"budget": 4`, 1)
	if code, out, _ := do(t, bad, "checklog", "-"); code != exitFailure || !strings.HasPrefix(out, "invalid:") {
		t.Fatalf("tampered log: exit %d, out %q", code, out)
	}
	for _, args := range [][]string{{"sim"}, {"sim", "--entity", "noequals"}, {"sim", "--entity", "a=((", "--ticks", "1"}, {"sim", "--entity", "a=K", "--sc", "maybe"}, {"checklog"}} {
		if code, _, _ := do(t, "", args...); code != exitUsage {
			t.Errorf("%v: exit %d", args, code)
		}
	}
}

func TestCompile(t *testing.T) {
	code, out, _ := do(t, "", "compile", `\x y. x`)
	if code != exitOK || strings.TrimSpace(out) != "K" {
		t.Fatalf("exit %d, output %q", code, out)
	}
	// What compile prints must reduce to the expected result.
	code, out, _ = do(t, "", "compile", `(\x. x x) y`, "--def", "y=a")
	if code != exitOK {
		t.Fatalf("exit %d", code)
	}
	code, out, _ = do(t, "", "reduce", strings.TrimSpace(out))
	if code != exitOK || !strings.HasPrefix(out, "a a\n") {
		t.Fatalf("exit %d, output %q", code, out)
	}
	if code, _, _ := do(t, "", "compile", `\x.`); code != exitFailure {
		t.Fatalf("bad lambda gave exit %d", code)
	}
	if code, _, _ := do(t, "", "compile"); code != exitUsage {
		t.Fatalf("missing argument gave exit %d", code)
	}
}

func TestSettledScopeFlag(t *testing.T) {
	// Under the settled mode the Sc step waits for the inner Abs, and the
	// result is the same normal form as under the safe mode.
	for _, sc := range []string{"safe", "settled"} {
		code, out, _ := do(t, "", "reduce", "sc_x (<I>_x a)", "--absorb", "s", "--sc", sc, "--perm", "x=K")
		if code != exitOK || !strings.Contains(out, "<<a>_x>_x") {
			t.Fatalf("--sc %s: exit %d, output %q", sc, code, out)
		}
	}
	if code, _, _ := do(t, "", "reduce", "a", "--sc", "bogus"); code != exitUsage && code != exitFailure {
		t.Fatalf("bad scope mode gave exit %d", code)
	}
}
