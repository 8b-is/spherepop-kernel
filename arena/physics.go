package arena

// Step advances the world one tick. Ships move in identifier order, and each
// ship moves along X, then Y, then Z, so the result does not depend on the
// order of anything a program did within a frame.
func (w *World) Step() {
	for _, id := range w.shipIDs {
		s := w.ships[id]
		for a := 0; a < 3; a++ {
			t := clamp32(s.thrust[a], -1, 1)
			v := s.Vel[a] + t*w.p.Accel
			v = v * 7 / 8 // damping; integer division truncates toward zero, so speed reaches 0
			s.Vel[a] = clamp32(v, -w.p.MaxSpeed, w.p.MaxSpeed)
		}
		s.thrust = Vec{}
		for a := Axis(0); a < 3; a++ {
			w.moveAxis(s, a)
		}
	}
	w.tick++
}

// openPortal finds an open portal on the given face of a cell that a ship at
// pos can fit through. upper selects the face at the cell's Max.
func (w *World) openPortal(cell string, axis Axis, upper bool, pos Vec) *Portal {
	o1, o2 := otherAxes(axis)
	r := w.p.Radius
	for i := range w.portals {
		pt := &w.portals[i]
		if pt.Axis != axis || (upper && pt.A != cell) || (!upper && pt.B != cell) {
			continue
		}
		if pt.Door != 0 && !w.doors[pt.Door] {
			continue
		}
		if pos[o1] >= pt.Lo[0]+r && pos[o1] <= pt.Hi[0]-r && pos[o2] >= pt.Lo[1]+r && pos[o2] <= pt.Hi[1]-r {
			return pt
		}
	}
	return nil
}

func (w *World) moveAxis(s *Ship, a Axis) {
	r := w.p.Radius
	v := s.Vel[a]
	c := w.cells[s.Cell]
	lo, hi := c.Min[a]+r, c.Max[a]-r
	// A ship whose centre may pass through an open portal is allowed to reach
	// the plane itself; anywhere else its surface stops at the wall.
	if w.openPortal(s.Cell, a, true, s.Pos) != nil {
		hi = c.Max[a]
	}
	if w.openPortal(s.Cell, a, false, s.Pos) != nil {
		lo = c.Min[a]
	}
	target := s.Pos[a] + v
	switch {
	case v > 0 && target > c.Max[a] && hi == c.Max[a]:
		pt := w.openPortal(s.Cell, a, true, s.Pos)
		s.Cell = pt.B
		n := w.cells[pt.B]
		w.settle(s, a, target, n.Min[a]+r, n.Max[a]-r)
	case v < 0 && target < c.Min[a] && lo == c.Min[a]:
		pt := w.openPortal(s.Cell, a, false, s.Pos)
		s.Cell = pt.A
		n := w.cells[pt.A]
		w.settle(s, a, target, n.Min[a]+r, n.Max[a]-r)
	default:
		w.settle(s, a, target, lo, hi)
	}
}

// settle places the ship at target, clamped to [lo, hi], and stops it on that
// axis if it hit the limit. A door that closes on a ship pushes it clear.
func (w *World) settle(s *Ship, a Axis, target, lo, hi int32) {
	p := clamp32(target, lo, hi)
	if p != target {
		s.Vel[a] = 0
	}
	s.Pos[a] = p
}
