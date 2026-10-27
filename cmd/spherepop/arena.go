package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/standardgalactic/spherepop-kernel/arena"
	"github.com/standardgalactic/spherepop-kernel/driver"
	"github.com/standardgalactic/spherepop-kernel/effects"
	"github.com/standardgalactic/spherepop-kernel/lambda"
	"github.com/standardgalactic/spherepop-kernel/reduce"
	"github.com/standardgalactic/spherepop-kernel/sched"
	"github.com/standardgalactic/spherepop-kernel/term"
)

// cmdArena runs one behavior, written in lambda syntax, in the built-in
// arena for a number of frames. It is a demonstration host: the language
// does not depend on it.
func cmdArena(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("arena", flag.ContinueOnError)
	fs.SetOutput(stderr)
	frames := fs.Int("frames", 60, "number of frames to run")
	budget := fs.Uint64("budget", 2000, "reduction steps granted per frame")
	strat := fs.String("strategy", "lo", "lo, li, ri or ro")
	registers := fs.Bool("registers", false, "pass the ship's register to the program as a ninth argument")
	absorb := fs.String("absorb", "", "absorption mode (default: the host default)")
	sc := fs.String("sc", "", "scope-constructor mode (default: the host default)")
	verbose := fs.Bool("v", false, "print the ship's state after every frame")
	effOut := fs.String("effects", "", "write the effects log to this file, one JSON entry per line")
	schedOut := fs.String("sched", "", "write the scheduler log to this file")
	var defs listFlag
	fs.Var(&defs, "def", "NAME=TERM abbreviation (repeatable)")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return exitUsage
	}
	if len(pos) != 1 || *frames < 0 {
		fmt.Fprintln(stderr, "spherepop arena: expected exactly one FILE argument (or - for standard input)")
		return exitUsage
	}
	s, ok := reduce.ParseStrategy(*strat)
	if !ok {
		fmt.Fprintf(stderr, "spherepop arena: unknown strategy %q\n", *strat)
		return exitUsage
	}
	cfg := sched.HostConfig()
	if *absorb != "" {
		if cfg.Absorb, err = reduce.ParseAbsorption(*absorb); err != nil {
			fmt.Fprintf(stderr, "spherepop arena: %v\n", err)
			return exitUsage
		}
	}
	if *sc != "" {
		if cfg.Sc, err = reduce.ParseScopeMode(*sc); err != nil {
			fmt.Fprintf(stderr, "spherepop arena: %v\n", err)
			return exitUsage
		}
	}
	dm := term.Defs{}
	for _, d := range defs {
		name, src, ok := strings.Cut(d, "=")
		if !ok || name == "" {
			fmt.Fprintf(stderr, "spherepop arena: --def wants NAME=TERM, got %q\n", d)
			return exitUsage
		}
		t, err := term.ParseWith(src, dm)
		if err != nil {
			fmt.Fprintf(stderr, "spherepop arena: --def %s: %v\n", name, err)
			return exitUsage
		}
		dm[name] = t
	}
	var src []byte
	if pos[0] == "-" {
		src, err = io.ReadAll(stdin)
	} else {
		src, err = os.ReadFile(pos[0])
	}
	if err != nil {
		fmt.Fprintf(stderr, "spherepop arena: %v\n", err)
		return exitFailure
	}
	prog, err := lambda.Compile(string(src), dm)
	if err != nil {
		fmt.Fprintf(stderr, "spherepop arena: %v\n", err)
		return exitFailure
	}

	w, err := arena.Demo()
	if err != nil {
		fmt.Fprintf(stderr, "spherepop arena: %v\n", err)
		return exitFailure
	}
	var sink io.Writer
	var effFile *os.File
	if *effOut != "" {
		if effFile, err = os.Create(*effOut); err != nil {
			fmt.Fprintf(stderr, "spherepop arena: %v\n", err)
			return exitFailure
		}
		defer effFile.Close()
		sink = effFile
	}
	host := &effects.Host{Schema: arena.Schema(), Auth: arena.DemoPolicy(), World: w, Log: effects.NewLog(sink)}
	d, err := driver.New(driver.Params{
		Reduce:   cfg,
		Policy:   sched.Policy{Strategy: s, Budget: *budget},
		Host:     host,
		Observer: arena.Observer{W: w, GoalCell: arena.DemoGoal, Door: arena.DemoDoor, Registers: *registers},
		Advance:  func() error { w.Step(); return w.Check() },
	}, []driver.Program{{ID: arena.DemoShip, Term: prog}})
	if err != nil {
		fmt.Fprintf(stderr, "spherepop arena: %v\n", err)
		return exitFailure
	}
	line := func(frame int) {
		sh, _ := w.Ship(arena.DemoShip)
		open, _ := w.DoorOpen(arena.DemoDoor)
		fmt.Fprintf(stdout, "frame %-4d cell=%s pos=%v vel=%v door=%t reg=%d\n", frame, sh.Cell, sh.Pos, sh.Vel, open, sh.Reg)
	}
	for i := 0; i < *frames; i++ {
		if err := d.Step(); err != nil {
			fmt.Fprintf(stderr, "spherepop arena: frame %d: %v\n", i, err)
			return exitFailure
		}
		if *verbose {
			line(i)
		}
	}
	if !*verbose {
		line(*frames - 1)
	}
	counts := map[effects.Disposition]int{}
	for _, e := range host.Log.Entries() {
		counts[e.Disposition]++
	}
	var names []string
	for k := range counts {
		names = append(names, string(k))
	}
	sort.Strings(names)
	var parts []string
	for _, k := range names {
		parts = append(parts, fmt.Sprintf("%s=%d", k, counts[effects.Disposition(k)]))
	}
	fmt.Fprintf(stdout, "effects: %s\n", strings.Join(parts, " "))
	fmt.Fprintf(stdout, "effects log head: %s\n", host.Log.Head())
	if *schedOut != "" {
		b, err := sched.Marshal(d.Scheduler().Log())
		if err == nil {
			err = os.WriteFile(*schedOut, b, 0o644)
		}
		if err != nil {
			fmt.Fprintf(stderr, "spherepop arena: %v\n", err)
			return exitFailure
		}
	}
	return exitOK
}

// cmdCheckEffects verifies an effects log written by `arena --effects`
// against the arena schema, and against an anchor if one is given.
func cmdCheckEffects(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("checkeffects", flag.ContinueOnError)
	fs.SetOutput(stderr)
	anchor := fs.String("anchor", "", "head hash held outside the log; detects a removed tail")
	schemaFile := fs.String("schema", "", "schema JSON file (default: the arena schema)")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return exitUsage
	}
	if len(pos) != 1 {
		fmt.Fprintln(stderr, "spherepop checkeffects: expected exactly one FILE argument (or - for standard input)")
		return exitUsage
	}
	var r io.Reader = stdin
	if pos[0] != "-" {
		f, err := os.Open(pos[0])
		if err != nil {
			fmt.Fprintf(stderr, "spherepop checkeffects: %v\n", err)
			return exitFailure
		}
		defer f.Close()
		r = f
	}
	schema := arena.Schema()
	if *schemaFile != "" {
		b, rerr := os.ReadFile(*schemaFile)
		if rerr == nil {
			schema, rerr = effects.ParseSchema(b)
		}
		if rerr != nil {
			fmt.Fprintf(stderr, "spherepop checkeffects: %v\n", rerr)
			return exitFailure
		}
	}
	entries, err := effects.ReadJSONL(r)
	if err == nil {
		err = effects.VerifyAgainst(entries, schema)
	}
	if err == nil && *anchor != "" {
		err = effects.VerifyAnchored(entries, *anchor)
	}
	if err != nil {
		fmt.Fprintf(stdout, "invalid: %v\n", err)
		return exitFailure
	}
	fmt.Fprintf(stdout, "valid: %d effects entries", len(entries))
	if *anchor != "" {
		fmt.Fprint(stdout, ", head matches the anchor")
	}
	fmt.Fprintln(stdout)
	return exitOK
}

// cmdArenaSchema prints the arena's effect schema as JSON, the form that
// `checkeffects --schema` and the reference verifier read.
func cmdArenaSchema(stdout, stderr io.Writer) int {
	b, err := effects.MarshalSchema(arena.Schema())
	if err != nil {
		fmt.Fprintf(stderr, "spherepop arena-schema: %v\n", err)
		return exitFailure
	}
	stdout.Write(b)
	return exitOK
}
