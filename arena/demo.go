package arena

import "github.com/standardgalactic/spherepop-kernel/effects"

// DemoShip and DemoGoal name the ship and the goal cell of the built-in level.
const (
	DemoShip = "s1"
	DemoGoal = "b"
	DemoDoor = 1
)

// Demo returns the built-in level: two boxes joined by a portal that door 1
// gates, with one ship in the first box and the goal in the second.
func Demo() (*World, error) {
	return New(DefaultParams(),
		[]Cell{
			{ID: "a", Min: Vec{0, 0, 0}, Max: Vec{40, 20, 20}},
			{ID: "b", Min: Vec{40, 0, 0}, Max: Vec{80, 20, 20}},
		},
		[]Portal{{ID: "p", A: "a", B: "b", Axis: X, Lo: [2]int32{4, 4}, Hi: [2]int32{16, 16}, Door: DemoDoor}},
		[]uint32{DemoDoor},
		[]Ship{{ID: DemoShip, Cell: "a", Pos: Vec{10, 10, 10}}})
}

// DemoPolicy grants the demo ship the verbs the schema offers.
func DemoPolicy() Authorizer {
	return Authorizer{Base: demoGrants()}
}

func demoGrants() *effects.Policy {
	p := &effects.Policy{}
	return p.Grant(DemoShip, "door", "open").Grant(DemoShip, "door", "close").
		Grant(DemoShip, "ship", "thrust").Grant(DemoShip, "ship", "set")
}
