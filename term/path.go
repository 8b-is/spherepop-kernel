package term

import (
	"errors"
	"fmt"
)

// Path addresses a subterm. Each character is one step down from the current
// node: 'L' to the function child of an application, 'R' to its argument,
// and 'B' into the body of a bracket. The empty path is the root.
type Path string

// The three step characters.
const (
	StepFun  byte = 'L'
	StepArg  byte = 'R'
	StepBody byte = 'B'
)

// Down returns p extended by one step.
func (p Path) Down(step byte) Path { return p + Path(string(step)) }

// ErrBadPath reports a path that does not lead to a subterm.
var ErrBadPath = errors.New("term: path does not lead to a subterm")

// At returns the subterm of t at path p.
func (t *Term) At(p Path) (*Term, error) {
	cur := t
	for i := 0; i < len(p); i++ {
		switch {
		case p[i] == StepFun && cur.kind == KindApp:
			cur = cur.left
		case p[i] == StepArg && cur.kind == KindApp:
			cur = cur.right
		case p[i] == StepBody && cur.kind == KindBracket:
			cur = cur.left
		default:
			return nil, fmt.Errorf("%w: step %d (%q) of %q", ErrBadPath, i, p[i], string(p))
		}
	}
	return cur, nil
}

// Replace returns the term obtained from t by replacing the subterm at path p
// with r. Everything off the path is shared with t.
func (t *Term) Replace(p Path, r *Term) (*Term, error) {
	return replace(t, p, 0, r)
}

func replace(t *Term, p Path, i int, r *Term) (*Term, error) {
	if i == len(p) {
		return r, nil
	}
	switch {
	case p[i] == StepFun && t.kind == KindApp:
		f, err := replace(t.left, p, i+1, r)
		if err != nil {
			return nil, err
		}
		return App(f, t.right), nil
	case p[i] == StepArg && t.kind == KindApp:
		x, err := replace(t.right, p, i+1, r)
		if err != nil {
			return nil, err
		}
		return App(t.left, x), nil
	case p[i] == StepBody && t.kind == KindBracket:
		b, err := replace(t.left, p, i+1, r)
		if err != nil {
			return nil, err
		}
		return Bracket(t.name, b), nil
	}
	return nil, fmt.Errorf("%w: step %d (%q) of %q", ErrBadPath, i, p[i], string(p))
}
