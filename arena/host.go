package arena

import (
	"fmt"
	"strconv"

	"github.com/standardgalactic/spherepop-kernel/effects"
	"github.com/standardgalactic/spherepop-kernel/encode"
	"github.com/standardgalactic/spherepop-kernel/term"
)

// Schema is the arena's effect vocabulary:
//
//	effect (door N) open | close
//	effect (ship S) thrust east|west|north|south|up|down
func Schema() effects.Schema {
	return effects.Schema{Targets: map[string]effects.TargetSpec{
		"door": {ID: effects.Int, Verbs: map[string]effects.VerbSpec{
			"open": {}, "close": {},
		}},
		"ship": {ID: effects.Sym, Verbs: map[string]effects.VerbSpec{
			"thrust": {Args: []effects.ArgType{effects.Sym}},
			"set":    {Args: []effects.ArgType{effects.Int}},
		}},
	}}
}

var dirs = map[string]struct {
	axis Axis
	sign int32
}{
	"east": {X, 1}, "west": {X, -1}, "north": {Y, 1}, "south": {Y, -1}, "up": {Z, 1}, "down": {Z, -1},
}

// Commit implements effects.World. It refuses, without changing anything, a
// request that names something that does not exist.
func (w *World) Commit(r effects.Request) error {
	switch r.Kind {
	case "door":
		n, err := strconv.ParseUint(r.ID, 10, 32)
		if err != nil {
			return fmt.Errorf("arena: bad door %q", r.ID)
		}
		switch r.Verb {
		case "open", "close":
			if !w.SetDoor(uint32(n), r.Verb == "open") {
				return fmt.Errorf("arena: no door %d", n)
			}
			return nil
		}
	case "ship":
		s := w.ships[r.ID]
		if s == nil {
			return fmt.Errorf("arena: no ship %q", r.ID)
		}
		if r.Verb == "set" && len(r.Args) == 1 {
			n, err := strconv.ParseUint(r.Args[0], 10, 32)
			if err != nil || n > MaxReg {
				return fmt.Errorf("arena: register value %q out of range", r.Args[0])
			}
			s.Reg = uint32(n)
			return nil
		}
		if r.Verb == "thrust" && len(r.Args) == 1 {
			d, ok := dirs[r.Args[0]]
			if !ok {
				return fmt.Errorf("arena: no direction %q", r.Args[0])
			}
			s.thrust[d.axis] += d.sign
			return nil
		}
	}
	return fmt.Errorf("arena: unsupported request %s", r)
}

// MaxReg bounds a register so that its Church numeral stays small.
const MaxReg = 64

// Authorizer lets a ship thrust only itself, and delegates everything else to
// a fixed grant list. It is the only place that decides who may do what.
type Authorizer struct {
	Base *effects.Policy
}

// Authorize implements effects.Authorizer.
func (a Authorizer) Authorize(subject string, r effects.Request) effects.Decision {
	if r.Kind == "ship" && r.ID != subject {
		return effects.Decision{Reason: fmt.Sprintf("%s may not command ship %s", subject, r.ID)}
	}
	return a.Base.Authorize(subject, r)
}

// Observer reports a ship's situation to its program as Church booleans, in
// this order: goal is east, goal is west, goal is north, goal is south, goal is
// up, goal is down (each strictly beyond the ship's position on that axis,
// within the ship's own cell or, if the goal is in another cell, toward the
// goal's cell centre), ship is in the goal cell, door 1 is open.
type Observer struct {
	W        *World
	GoalCell string
	Door     uint32
	// Registers appends the ship's register, as a Church numeral, after the
	// eight Booleans. Programs written for eight arguments need it off.
	Registers bool
}

// Observe implements driver.Observer. The subject is a ship identifier.
func (o Observer) Observe(subject string, frame int) ([]*term.Term, error) {
	s, ok := o.W.Ship(subject)
	if !ok {
		return nil, fmt.Errorf("arena: no ship %q", subject)
	}
	g, ok := o.W.CellOf(o.GoalCell)
	if !ok {
		return nil, fmt.Errorf("arena: no goal cell %q", o.GoalCell)
	}
	var centre Vec
	for a := 0; a < 3; a++ {
		centre[a] = (g.Min[a] + g.Max[a]) / 2
	}
	open, _ := o.W.DoorOpen(o.Door)
	obs := []*term.Term{
		encode.Bool(centre[X] > s.Pos[X]), encode.Bool(centre[X] < s.Pos[X]),
		encode.Bool(centre[Y] > s.Pos[Y]), encode.Bool(centre[Y] < s.Pos[Y]),
		encode.Bool(centre[Z] > s.Pos[Z]), encode.Bool(centre[Z] < s.Pos[Z]),
		encode.Bool(s.Cell == o.GoalCell), encode.Bool(open),
	}
	if o.Registers {
		n, err := encode.Nat(s.Reg)
		if err != nil {
			return nil, err
		}
		obs = append(obs, n)
	}
	return obs, nil
}

// CommitAll implements effects.AtomicWorld. It applies the requests in order
// and, if any is refused, restores the doors and ships it started from.
func (w *World) CommitAll(rs []effects.Request) error {
	doors := make(map[uint32]bool, len(w.doors))
	for k, v := range w.doors {
		doors[k] = v
	}
	ships := make(map[string]Ship, len(w.ships))
	for k, s := range w.ships {
		ships[k] = *s
	}
	for i, r := range rs {
		if err := w.Commit(r); err != nil {
			w.doors = doors
			for k, s := range ships {
				*w.ships[k] = s
			}
			return fmt.Errorf("group member %d: %w", i, err)
		}
	}
	return nil
}
