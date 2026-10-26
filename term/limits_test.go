package term

import (
	"errors"
	"strings"
	"testing"
)

func TestHostileSourceIsRejected(t *testing.T) {
	var se *SyntaxError
	deep := strings.Repeat("(", MaxNesting+1) + "a" + strings.Repeat(")", MaxNesting+1)
	if _, err := Parse(deep); !errors.As(err, &se) {
		t.Errorf("deep nesting: %v", err)
	}
	brackets := strings.Repeat("<", MaxNesting+1)
	if _, err := Parse(brackets); !errors.As(err, &se) {
		t.Errorf("deep brackets: %v", err)
	}
	if _, err := Parse(strings.Repeat("a ", MaxSourceBytes)); !errors.As(err, &se) {
		t.Errorf("oversize source: %v", err)
	}
	// Nesting at the limit is still accepted, and the counter unwinds, so a
	// long flat sequence of groups is not mistaken for deep nesting.
	if _, err := Parse(strings.Repeat("(a) ", 200000)); err != nil {
		t.Errorf("flat groups rejected: %v", err)
	}
}
