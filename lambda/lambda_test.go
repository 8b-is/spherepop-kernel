package lambda

import (
	"errors"
	"math/rand"
	"strings"
	"testing"

	"github.com/standardgalactic/spherepop-kernel/reduce"
	"github.com/standardgalactic/spherepop-kernel/term"
)

func nf(t *testing.T, x *term.Term) string {
	t.Helper()
	res, err := reduce.Reduce(x, reduce.LeftmostOutermost, 100000)
	if err != nil || res.Exhausted {
		t.Fatalf("%v exhausted=%v on %s", err, res.Exhausted, x)
	}
	return res.Term.String()
}

// run compiles src and applies it to atom arguments.
func run(t *testing.T, src string, args ...string) string {
	t.Helper()
	c, err := Compile(src, nil)
	if err != nil {
		t.Fatalf("%q: %v", src, err)
	}
	for _, a := range args {
		c = term.App(c, term.MustParse(a))
	}
	return nf(t, c)
}

func TestBasicFunctions(t *testing.T) {
	cases := []struct {
		src  string
		args []string
		want string
	}{
		{`\x. x`, []string{"a"}, "a"},
		{`\x y. x`, []string{"a", "b"}, "a"},
		{`\x y. y`, []string{"a", "b"}, "b"},
		{`\f x. f (f x)`, []string{"g", "a"}, "g (g a)"},
		{`\x. pair x x`, []string{"a"}, "pair a a"},
		{`\x y z. x z (y z)`, []string{"a", "b", "c"}, "a c (b c)"},
		{`\x. \y. y x`, []string{"a", "b"}, "b a"},
		{`\x. effects (effect (door 1) open) x`, []string{"r"}, "effects (effect (door 1) open) r"},
		{`\x. x (\y. y)`, []string{"f"}, "f I"}, // inner identity compiles to I
		{`(\x. x) a`, nil, "a"},
		{`(\x y. x) a b`, nil, "a"},
		{`\x. K x`, []string{"a", "b"}, "a"},
	}
	for _, tc := range cases {
		if got := run(t, tc.src, tc.args...); got != tc.want {
			t.Errorf("%s %v = %s, want %s", tc.src, tc.args, got, tc.want)
		}
	}
}

func TestShadowing(t *testing.T) {
	// The inner x hides the outer one.
	if got := run(t, `\x. (\x. x)`, "a", "b"); got != "b" {
		t.Errorf("inner binding should win: %s", got)
	}
	if got := run(t, `\x y. (\x. y x) x`, "a", "b"); got != "b a" {
		t.Errorf("%s", got)
	}
}

func TestChurchBooleansAndNumeralsCompose(t *testing.T) {
	// not = \b. b false true, using K I for false.
	if got := run(t, `\b x y. b y x`, "K", "p", "q"); got != "q" {
		t.Errorf("swap with true: %s", got)
	}
	// 2 f x with Church 2 written in the language itself.
	if got := run(t, `(\n f x. n f x) (\f x. f (f x))`, "g", "a"); got != "g (g a)" {
		t.Errorf("%s", got)
	}
}

func TestDefsAndConstants(t *testing.T) {
	defs := term.Defs{"greet": term.MustParse("hello world")}
	c, err := Compile(`\x. greet x`, defs)
	if err != nil {
		t.Fatal(err)
	}
	if got := nf(t, term.App(c, term.Atom("a"))); got != "hello world a" {
		t.Errorf("%s", got)
	}
	if got := run(t, `\x. W K x`, "a"); got != "a" {
		t.Errorf("explicit constants: %s", got)
	}
}

func TestSyntaxErrors(t *testing.T) {
	for _, src := range []string{
		``, `\`, `\x`, `\x.`, `\. x`, `\X. x`, `\x. (`, `\x. x )`, `(`, `)`, `\x. <x>_l`,
		`\x. sc_l x`, `\x. Z`, `\x. 3a`, `\ x y`, `x $`, `\x. x \y`,
	} {
		_, err := Compile(src, nil)
		var se *SyntaxError
		if !errors.As(err, &se) {
			t.Errorf("%q: want SyntaxError, got %v", src, err)
		}
	}
	if _, err := Compile(strings.Repeat("(", MaxDepth+5)+"a"+strings.Repeat(")", MaxDepth+5), nil); !errors.Is(err, ErrTooDeep) {
		t.Errorf("deep nesting: %v", err)
	}
	if _, err := Compile(strings.Repeat("a ", MaxSourceBytes), nil); err == nil {
		t.Error("oversized source accepted")
	}
}

func TestOutputUsesOnlyReducerConstants(t *testing.T) {
	c, err := Compile(`\f g x. f x (g x)`, nil)
	if err != nil {
		t.Fatal(err)
	}
	var walk func(*term.Term)
	walk = func(u *term.Term) {
		switch u.Kind() {
		case term.KindApp:
			walk(u.Fun())
			walk(u.Arg())
		case term.KindConst:
			if strings.IndexByte("SKIBCW", u.Name()[0]) < 0 {
				t.Errorf("unexpected constant %s", u.Name())
			}
		case term.KindBracket, term.KindScope:
			t.Errorf("compiled output contains %s", u.Kind())
		}
	}
	walk(c)
}

// Property: for a random applicative body over parameters and atoms, the
// compiled function applied to atom arguments reduces to exactly the body with
// the arguments substituted. This is beta reduction checked through the SK
// machinery, on many shapes including shared and repeated variables.
func TestCompileMatchesSubstitution(t *testing.T) {
	r := rand.New(rand.NewSource(11))
	params := []string{"x", "y", "z"}
	atoms := []string{"a", "b", "c", "d"}
	var gen func(depth int) string
	gen = func(depth int) string {
		if depth == 0 || r.Intn(4) == 0 {
			if r.Intn(2) == 0 {
				return params[r.Intn(len(params))]
			}
			return atoms[r.Intn(len(atoms))]
		}
		return "(" + gen(depth-1) + " " + gen(depth-1) + ")"
	}
	for i := 0; i < 600; i++ {
		body := gen(1 + r.Intn(5))
		n := 1 + r.Intn(3)
		src := `\` + strings.Join(params[:n], " ") + `. ` + body
		args := []string{"p", "q", "r"}[:n]

		m := map[string]*term.Term{}
		for j := 0; j < n; j++ {
			m[params[j]] = term.Atom(args[j])
		}
		// Parameters beyond n are not bound, so they stay atoms, exactly as
		// the compiler treats them.
		want := term.Subst(term.MustParse(body), m).String()

		c, err := Compile(src, nil)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		for _, a := range args {
			c = term.App(c, term.Atom(a))
		}
		if got := nf(t, c); got != want {
			t.Fatalf("%s %v\n got %s\nwant %s", src, args, got, want)
		}
	}
}

func TestEtaAndSizes(t *testing.T) {
	c, _ := Compile(`\x. f x`, nil)
	if c.String() != "f" {
		t.Errorf("eta contraction: %s", c)
	}
	// Size grows linearly with the number of parameters used, not exponentially.
	small, _ := Compile(`\a b c d. a b c d`, nil)
	big, _ := Compile(`\a b c d e f g h. a b c d e f g h`, nil)
	if big.Size() > 4*small.Size() {
		t.Errorf("sizes %d and %d", small.Size(), big.Size())
	}
}
