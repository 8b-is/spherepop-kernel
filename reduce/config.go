package reduce

import (
	"fmt"

	"github.com/standardgalactic/spherepop-kernel/term"
)

// Absorption selects the side condition on the Abs rule.
type Absorption uint8

// The absorption modes. AbsU is the default because it preserves confluence
// for every permission structure; AbsAny does so only when the root
// permission is contained in every label's permission.
const (
	AbsOff Absorption = iota // Abs is never applied
	AbsAny                   // unrestricted
	AbsNF                    // argument is R-normal (insufficient, Theorem 4.4)
	AbsS                     // argument is settled
	AbsU                     // argument is R-normal and settled or insensitive
)

var absNames = map[Absorption]string{AbsOff: "off", AbsAny: "any", AbsNF: "nf", AbsS: "s", AbsU: "u"}

func (a Absorption) String() string {
	if n, ok := absNames[a]; ok {
		return n
	}
	return fmt.Sprintf("absorption(%d)", uint8(a))
}

// ParseAbsorption reads a mode name and fails closed on anything else.
func ParseAbsorption(s string) (Absorption, error) {
	for a, n := range absNames {
		if n == s {
			return a, nil
		}
	}
	return 0, fmt.Errorf("reduce: unknown absorption mode %q (want off, any, nf, s or u)", s)
}

// ScopeMode selects the side condition on the Sc rule.
type ScopeMode uint8

// ScAny is the rule as the scoped-rules essay states it: always admissible,
// and therefore non-confluent whenever a label restricts the root permission
// (Theorem 4.2). ScSafe applies Sc only when its argument satisfies the same
// Safe criterion as AbsU. That restriction is not in the essays; it is
// status P here, supported only by randomized testing.
const (
	ScAny ScopeMode = iota + 1
	ScSafe
	ScSettled // research: the argument must be settled, which excludes the insensitive clause
)

func (m ScopeMode) String() string {
	switch m {
	case ScAny:
		return "any"
	case ScSafe:
		return "safe"
	case ScSettled:
		return "settled"
	}
	return fmt.Sprintf("scopemode(%d)", uint8(m))
}

// ParseScopeMode reads a mode name and fails closed on anything else.
func ParseScopeMode(s string) (ScopeMode, error) {
	for _, m := range []ScopeMode{ScAny, ScSafe, ScSettled} {
		if m.String() == s {
			return m, nil
		}
	}
	return 0, fmt.Errorf("reduce: unknown scope mode %q (want any, safe or settled)", s)
}

// Config fixes the permission structure and the absorption mode. It is the
// whole of the reducer's environment; nothing else can influence a step.
type Config struct {
	// Root is the permission at the root chain, pi(epsilon).
	Root PermSet
	// Perm maps each label to its permission, pi(label).
	Perm map[string]PermSet
	// Gated is the set of constants whose contraction needs permission.
	// A constant outside it is admissible everywhere.
	Gated PermSet
	// Absorb is the side condition on the Abs rule.
	Absorb Absorption
	// Sc is the side condition on the Sc rule. The zero value is read as ScAny.
	Sc ScopeMode
	// Trace asks Reduce to record every step.
	Trace bool
}

// DefaultConfig permits everything at the root, gates W, K and C, absorbs
// under AbsU and records a trace.
func DefaultConfig() Config {
	return Config{Root: PermAll, Gated: PermW | PermK | PermC, Absorb: AbsU, Trace: true}
}

func (c *Config) scMode() ScopeMode {
	if c.Sc == 0 {
		return ScAny
	}
	return c.Sc
}

// perm returns the permission of a label. An unknown label has the empty
// permission, so the reducer fails closed even when a caller skipped Validate.
func (c *Config) perm(label string) PermSet { return c.Perm[label] }

// Validate reports ErrUnknownLabel if t uses a label that has no entry.
func (c *Config) Validate(t *term.Term) error {
	for _, l := range t.Labels() {
		if _, ok := c.Perm[l]; !ok {
			return fmt.Errorf("%w: %q", ErrUnknownLabel, l)
		}
	}
	return nil
}

// admissible reports whether a contraction headed by the named constant is
// permitted under the effective permission mask.
func (c *Config) admissible(head string, mask PermSet) bool {
	bit, ok := permBit(head)
	if !ok || c.Gated&bit == 0 {
		return true
	}
	return mask&bit != 0
}

// ConfigSpec is the serialisable form of a Config. Names are sorted so the
// encoding is canonical.
type ConfigSpec struct {
	Root   []string            `json:"root"`
	Perm   map[string][]string `json:"perm,omitempty"`
	Gated  []string            `json:"gated"`
	Absorb string              `json:"absorb"`
	Sc     string              `json:"sc"`
}

// Spec returns the serialisable form of c.
func (c Config) Spec() ConfigSpec {
	s := ConfigSpec{Root: c.Root.Names(), Gated: c.Gated.Names(), Absorb: c.Absorb.String(), Sc: c.scMode().String()}
	if len(c.Perm) > 0 {
		s.Perm = make(map[string][]string, len(c.Perm))
		for _, l := range sortedKeys(c.Perm) {
			s.Perm[l] = c.Perm[l].Names()
		}
	}
	if s.Root == nil {
		s.Root = []string{}
	}
	if s.Gated == nil {
		s.Gated = []string{}
	}
	return s
}

// Config rebuilds a Config from its serialised form.
func (s ConfigSpec) Config() (Config, error) {
	var c Config
	var err error
	if c.Root, err = ParsePermSet(s.Root); err != nil {
		return c, err
	}
	if c.Gated, err = ParsePermSet(s.Gated); err != nil {
		return c, err
	}
	if c.Absorb, err = ParseAbsorption(s.Absorb); err != nil {
		return c, err
	}
	if c.Sc, err = ParseScopeMode(s.Sc); err != nil {
		return c, err
	}
	if len(s.Perm) > 0 {
		c.Perm = make(map[string]PermSet, len(s.Perm))
		for l, names := range s.Perm {
			if c.Perm[l], err = ParsePermSet(names); err != nil {
				return c, err
			}
		}
	}
	return c, nil
}
