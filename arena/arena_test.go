package arena

import (
	"testing"

	"github.com/standardgalactic/spherepop-kernel/driver"
	"github.com/standardgalactic/spherepop-kernel/effects"
	"github.com/standardgalactic/spherepop-kernel/lambda"
	"github.com/standardgalactic/spherepop-kernel/reduce"
	"github.com/standardgalactic/spherepop-kernel/sched"
	"github.com/standardgalactic/spherepop-kernel/term"
)

func level(t *testing.T) *World {
	t.Helper()
	w, err := New(DefaultParams(),
		[]Cell{
			{ID: "a", Min: Vec{0, 0, 0}, Max: Vec{40, 20, 20}},
			{ID: "b", Min: Vec{40, 0, 0}, Max: Vec{80, 20, 20}},
		},
		[]Portal{{ID: "p", A: "a", B: "b", Axis: X, Lo: [2]int32{4, 4}, Hi: [2]int32{16, 16}, Door: 1}},
		[]uint32{1},
		[]Ship{{ID: "s1", Cell: "a", Pos: Vec{10, 10, 10}}})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// A behavior written in lambda syntax: open the door and thrust east until the
// ship is in the goal cell, then close the door and stop acting.
const behavior = `\ge gw gn gs gu gd ing dopen.
  ing (effects (effect (door 1) close))
      (effects (effect (door 1) open) (effect (ship s1) thrust east))`

func rig(t *testing.T, w *World, src string, registers ...bool) (*driver.Driver, *effects.Host) {
	t.Helper()
	prog, err := lambda.Compile(src, nil)
	if err != nil {
		t.Fatal(err)
	}
	pol := &effects.Policy{}
	pol.Grant("s1", "door", "open").Grant("s1", "door", "close").Grant("s1", "ship", "thrust").Grant("s1", "ship", "set")
	host := &effects.Host{Schema: Schema(), Auth: Authorizer{Base: pol}, World: w, Log: effects.NewLog(nil)}
	d, err := driver.New(driver.Params{
		Reduce:   sched.HostConfig(),
		Policy:   sched.Policy{Strategy: reduce.LeftmostOutermost, Budget: 2000},
		Host:     host,
		Observer: Observer{W: w, GoalCell: "b", Door: 1, Registers: len(registers) > 0 && registers[0]},
		Advance:  func() error { w.Step(); return w.Check() },
	}, []driver.Program{{ID: "s1", Term: prog}})
	if err != nil {
		t.Fatal(err)
	}
	return d, host
}

func TestShipFliesThroughDoorToGoal(t *testing.T) {
	w := level(t)
	d, _ := rig(t, w, behavior)
	for i := 0; i < 60; i++ {
		if err := d.Step(); err != nil {
			t.Fatal(err)
		}
		if s, _ := w.Ship("s1"); s.Cell == "b" {
			break
		}
	}
	s, _ := w.Ship("s1")
	if s.Cell != "b" {
		t.Fatalf("ship ended in %q at %v", s.Cell, s.Pos)
	}
	if open, _ := w.DoorOpen(1); !open {
		// the door stays open until a round observes arrival
		t.Fatal("door should still be open on arrival frame")
	}
	if err := d.Step(); err != nil {
		t.Fatal(err)
	}
	if open, _ := w.DoorOpen(1); open {
		t.Fatal("door should close once the program sees it is in the goal cell")
	}
}

func TestClosedDoorBlocksShip(t *testing.T) {
	w := level(t)
	// Thrust east without ever opening the door.
	d, _ := rig(t, w, `\ge gw gn gs gu gd ing dopen. effects (effect (ship s1) thrust east)`)
	for i := 0; i < 80; i++ {
		if err := d.Step(); err != nil {
			t.Fatal(err)
		}
	}
	s, _ := w.Ship("s1")
	if s.Cell != "a" || s.Pos[X] != 36 {
		t.Fatalf("ship should rest against the wall at x=36, got %q %v", s.Cell, s.Pos)
	}
}

func TestOffCentreShipCannotUsePortal(t *testing.T) {
	// The opening is [4,16] on Y and Z; a ship of radius 4 fits only with its
	// centre in [8,12]. Each case violates one bound.
	for _, pos := range []Vec{{10, 6, 10}, {10, 14, 10}, {10, 10, 6}, {10, 10, 14}} {
		w := level(t)
		w.SetDoor(1, true)
		w.ships["s1"].Pos = pos
		for i := 0; i < 40; i++ {
			w.ships["s1"].thrust[X] = 1
			w.Step()
			if err := w.Check(); err != nil {
				t.Fatal(err)
			}
		}
		if s, _ := w.Ship("s1"); s.Cell != "a" {
			t.Fatalf("ship starting at %v passed a wall off-centre: %q %v", pos, s.Cell, s.Pos)
		}
	}
}

func TestShipCannotCommandAnother(t *testing.T) {
	w, err := New(DefaultParams(),
		[]Cell{{ID: "a", Min: Vec{0, 0, 0}, Max: Vec{40, 20, 20}}}, nil, nil,
		[]Ship{{ID: "s1", Cell: "a", Pos: Vec{10, 10, 10}}, {ID: "s2", Cell: "a", Pos: Vec{20, 10, 10}}})
	if err != nil {
		t.Fatal(err)
	}
	pol := &effects.Policy{}
	pol.Grant("s1", "ship", "thrust")
	host := &effects.Host{Schema: Schema(), Auth: Authorizer{Base: pol}, World: w, Log: effects.NewLog(nil)}
	ents, err := host.Handle(0, "s1", "", reduce.Result{Term: mustParse(t, "effects (effect (ship s2) thrust east)")})
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 || ents[0].Disposition != effects.Denied {
		t.Fatalf("want one denial, got %+v", ents)
	}
	if s, _ := w.Ship("s2"); s.thrust != (Vec{}) {
		t.Fatal("denied request changed the world")
	}
}

func TestRunsAreReproducible(t *testing.T) {
	run := func() (Ship, string, int) {
		w := level(t)
		d, host := rig(t, w, behavior)
		if err := d.Run(40); err != nil {
			t.Fatal(err)
		}
		s, _ := w.Ship("s1")
		if err := effects.Verify(host.Log.Entries()); err != nil {
			t.Fatal(err)
		}
		return s, host.Log.Head(), len(host.Log.Entries())
	}
	s1, h1, n1 := run()
	s2, h2, n2 := run()
	if s1 != s2 || h1 != h2 || n1 != n2 || n1 == 0 {
		t.Fatalf("runs differ: %+v/%s/%d vs %+v/%s/%d", s1, h1, n1, s2, h2, n2)
	}
}

func mustParse(t *testing.T, src string) *term.Term {
	t.Helper()
	x, err := term.Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	return x
}

func TestLevelValidation(t *testing.T) {
	cells := []Cell{{ID: "a", Min: Vec{0, 0, 0}, Max: Vec{40, 20, 20}}, {ID: "b", Min: Vec{41, 0, 0}, Max: Vec{80, 20, 20}}}
	if _, err := New(DefaultParams(), cells, []Portal{{ID: "p", A: "a", B: "b", Axis: X, Lo: [2]int32{4, 4}, Hi: [2]int32{16, 16}}}, nil, nil); err == nil {
		t.Fatal("portal between cells that do not meet was accepted")
	}
	if _, err := New(DefaultParams(), cells[:1], nil, nil, []Ship{{ID: "s", Cell: "a", Pos: Vec{1, 10, 10}}}); err == nil {
		t.Fatal("ship overlapping a wall was accepted")
	}
}

// A two-state behavior whose only memory is the register the host holds.
const stateful = `\ge gw gn gs gu gd ing dopen reg.
  reg (\z. effects)
      (ing (effects (effect (door 1) close) (effect (ship s1) set 1))
           (effects (effect (door 1) open) (effect (ship s1) thrust east)))`

func TestHostHeldMemoryCarriesStateBetweenRounds(t *testing.T) {
	w := level(t)
	d, _ := rig(t, w, stateful, true)
	for i := 0; i < 60; i++ {
		if err := d.Step(); err != nil {
			t.Fatal(err)
		}
		if s, _ := w.Ship("s1"); s.Reg == 1 {
			break
		}
	}
	s, _ := w.Ship("s1")
	if s.Reg != 1 || s.Cell != "b" {
		t.Fatalf("want register 1 in cell b, got reg %d in %q", s.Reg, s.Cell)
	}
	if err := d.Run(3); err != nil {
		t.Fatal(err)
	}
	if open, _ := w.DoorOpen(1); open {
		t.Fatal("door should have been closed by the arrival round")
	}
	// In state 1 the program proposes nothing, so an outside change to the
	// door is left alone. Without memory it would be closed again.
	w.SetDoor(1, true)
	if err := d.Run(5); err != nil {
		t.Fatal(err)
	}
	if open, _ := w.DoorOpen(1); !open {
		t.Fatal("idle state acted on the world")
	}
}

func TestRegisterBounds(t *testing.T) {
	w := level(t)
	for _, v := range []string{"65", "4294967296"} {
		if err := w.Commit(effects.Request{Kind: "ship", ID: "s1", Verb: "set", Args: []string{v}}); err == nil {
			t.Fatalf("register value %s accepted", v)
		}
	}
	if err := w.Commit(effects.Request{Kind: "ship", ID: "s1", Verb: "set", Args: []string{"64"}}); err != nil {
		t.Fatal(err)
	}
}
