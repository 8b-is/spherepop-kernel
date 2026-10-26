// Package arena is a minimal headless world for SpherePOP programs: boxes
// joined by door-gated portals, with spheres flying between them. It is a
// host, not part of the language. All arithmetic is integer, so a run is
// reproducible bit for bit on any machine, and nothing here knows about
// rendering, input devices, clocks or networks.
//
// The world offers three things to the layers above it. Commit applies an
// authorized effects.Request. Step advances physics by one fixed tick. Observer
// reports a ship's situation as the encoded arguments a program expects.
package arena

import (
	"errors"
	"fmt"
	"sort"
)

// Axis names a coordinate axis.
type Axis uint8

// The axes. East, north and up are the positive directions.
const (
	X Axis = iota
	Y
	Z
)

// Vec is a point or vector in integer units.
type Vec [3]int32

// Cell is an axis-aligned box.
type Cell struct {
	ID       string
	Min, Max Vec
}

// Portal joins two cells across a shared face. A is the cell on the lower side
// of Axis and B the cell on the upper side, so A.Max[Axis] equals B.Min[Axis].
// The opening is the rectangle [Lo, Hi] in the other two axes, in ascending
// axis order. Door is 0 for an opening that is always open, otherwise the
// number of the door that gates it.
type Portal struct {
	ID     string
	A, B   string
	Axis   Axis
	Lo, Hi [2]int32
	Door   uint32
}

// Ship is a sphere of the world's radius.
type Ship struct {
	ID     string
	Cell   string
	Pos    Vec
	Vel    Vec
	Reg    uint32 // host-held memory, set only by effect
	thrust Vec    // sum of this frame's thrust requests, consumed by Step
}

// Params fix the physics. All are in integer units per tick.
type Params struct {
	Radius   int32 // ship radius
	Accel    int32 // velocity added per axis by one thrust request
	MaxSpeed int32 // per-axis speed limit
}

// DefaultParams are small, round numbers suitable for tests and demos.
func DefaultParams() Params { return Params{Radius: 4, Accel: 2, MaxSpeed: 8} }

// Errors from building a world.
var ErrLevel = errors.New("arena: invalid level")

// World is the whole simulation state.
type World struct {
	p       Params
	cells   map[string]*Cell
	portals []Portal // sorted by ID
	doors   map[uint32]bool
	ships   map[string]*Ship
	shipIDs []string
	tick    int64
}

func levelErr(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrLevel, fmt.Sprintf(format, args...))
}

// New validates a level and returns a world with all doors closed.
func New(p Params, cells []Cell, portals []Portal, doors []uint32, ships []Ship) (*World, error) {
	if p.Radius <= 0 || p.Accel <= 0 || p.MaxSpeed <= 0 {
		return nil, levelErr("physics parameters must be positive: %+v", p)
	}
	w := &World{p: p, cells: map[string]*Cell{}, doors: map[uint32]bool{}, ships: map[string]*Ship{}}
	for i := range cells {
		c := cells[i]
		if c.ID == "" || w.cells[c.ID] != nil {
			return nil, levelErr("cell id %q is empty or repeated", c.ID)
		}
		for a := 0; a < 3; a++ {
			if c.Max[a]-c.Min[a] < 2*p.Radius {
				return nil, levelErr("cell %q is too small for a ship on axis %d", c.ID, a)
			}
		}
		w.cells[c.ID] = &c
	}
	for _, d := range doors {
		if d == 0 {
			return nil, levelErr("door numbers start at 1")
		}
		w.doors[d] = false
	}
	seen := map[string]bool{}
	for _, pt := range portals {
		a, b := w.cells[pt.A], w.cells[pt.B]
		if pt.ID == "" || seen[pt.ID] || a == nil || b == nil || pt.Axis > Z {
			return nil, levelErr("portal %q is invalid", pt.ID)
		}
		seen[pt.ID] = true
		if a.Max[pt.Axis] != b.Min[pt.Axis] {
			return nil, levelErr("portal %q: cells %q and %q do not meet on axis %d", pt.ID, pt.A, pt.B, pt.Axis)
		}
		o1, o2 := otherAxes(pt.Axis)
		for k, ax := range []int{o1, o2} {
			lo := max32(a.Min[ax], b.Min[ax])
			hi := min32(a.Max[ax], b.Max[ax])
			if pt.Lo[k] < lo || pt.Hi[k] > hi || pt.Hi[k]-pt.Lo[k] < 2*p.Radius {
				return nil, levelErr("portal %q: opening on axis %d must lie within [%d,%d] and fit a ship", pt.ID, ax, lo, hi)
			}
		}
		if _, ok := w.doors[pt.Door]; pt.Door != 0 && !ok {
			return nil, levelErr("portal %q uses undeclared door %d", pt.ID, pt.Door)
		}
		w.portals = append(w.portals, pt)
	}
	sort.Slice(w.portals, func(i, j int) bool { return w.portals[i].ID < w.portals[j].ID })
	for i := range ships {
		s := ships[i]
		c := w.cells[s.Cell]
		if s.ID == "" || w.ships[s.ID] != nil || c == nil {
			return nil, levelErr("ship %q is invalid", s.ID)
		}
		for a := 0; a < 3; a++ {
			if s.Pos[a] < c.Min[a]+p.Radius || s.Pos[a] > c.Max[a]-p.Radius {
				return nil, levelErr("ship %q starts outside cell %q", s.ID, s.Cell)
			}
		}
		s.thrust = Vec{}
		w.ships[s.ID] = &s
		w.shipIDs = append(w.shipIDs, s.ID)
	}
	sort.Strings(w.shipIDs)
	return w, nil
}

func otherAxes(a Axis) (int, int) {
	switch a {
	case X:
		return 1, 2
	case Y:
		return 0, 2
	}
	return 0, 1
}

func min32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}
func max32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}
func clamp32(v, lo, hi int32) int32 { return max32(lo, min32(hi, v)) }

// Tick returns the number of physics steps taken.
func (w *World) Tick() int64 { return w.tick }

// ShipIDs returns ship identifiers in order.
func (w *World) ShipIDs() []string { return append([]string(nil), w.shipIDs...) }

// Ship returns a copy of a ship's state.
func (w *World) Ship(id string) (Ship, bool) {
	s, ok := w.ships[id]
	if !ok {
		return Ship{}, false
	}
	return *s, true
}

// CellOf returns a cell's bounds.
func (w *World) CellOf(id string) (Cell, bool) {
	c, ok := w.cells[id]
	if !ok {
		return Cell{}, false
	}
	return *c, true
}

// DoorOpen reports a door's state. The second result is false for an unknown door.
func (w *World) DoorOpen(n uint32) (open, known bool) {
	o, ok := w.doors[n]
	return o, ok
}

// SetDoor changes a door. It reports false for an unknown door.
func (w *World) SetDoor(n uint32, open bool) bool {
	if _, ok := w.doors[n]; !ok {
		return false
	}
	w.doors[n] = open
	return true
}

// Check verifies the invariants of the world: every ship lies inside its cell,
// within speed limits. A violation is a bug in the physics, never in a program.
func (w *World) Check() error {
	for _, id := range w.shipIDs {
		s := w.ships[id]
		c := w.cells[s.Cell]
		if c == nil {
			return fmt.Errorf("arena: ship %q is in unknown cell %q", id, s.Cell)
		}
		for a := 0; a < 3; a++ {
			if s.Pos[a] < c.Min[a] || s.Pos[a] > c.Max[a] {
				return fmt.Errorf("arena: ship %q at %v is outside cell %q", id, s.Pos, c.ID)
			}
			if s.Vel[a] > w.p.MaxSpeed || s.Vel[a] < -w.p.MaxSpeed {
				return fmt.Errorf("arena: ship %q exceeds the speed limit: %v", id, s.Vel)
			}
		}
	}
	return nil
}
