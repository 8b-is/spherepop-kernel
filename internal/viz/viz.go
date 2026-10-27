// Package viz closes the loop of the seed for spherepop-kernel
// (inspired by 8b-is/enthea#1 with @standardgalactic):
//
//	spherepop-kernel (the pure-stdlib reduction engine door)
//	  x
//	index.html (the single-file WebCrypto Circle-Calculus & Merkle-trace surface)
//
// Every reduction step or term state maps deterministically via a pure function
// MapStep(StepEvent) -> VisualState {Hue, Intensity, Complexity} so the expected
// visual state is derivable directly from the trace event without running a
// browser or renderer, and is served over pure-stdlib net/http ("/state" JSON
// and "/events" text/event-stream SSE).
package viz

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"math/bits"
	"net/http"
	"strings"

	"github.com/standardgalactic/spherepop-kernel/reduce"
)

// SizeSaturation is the term application-circle size at which visual
// complexity saturates at 1.0.
const SizeSaturation = 64

// StepEvent is the structured engine event emitted at a reduction step or normal form.
type StepEvent struct {
	Rule       reduce.RuleID  `json:"rule"`
	PermMask   reduce.PermSet `json:"perm_mask"`
	TermSize   int            `json:"term_size"`
	TritLength int            `json:"trit_length"`
	Digest     string         `json:"digest,omitempty"`
}

// VisualState is the deterministic projection of a StepEvent onto three
// visual parameters, derivable without running the browser surface.
type VisualState struct {
	Event      StepEvent `json:"event"`
	Hue        float64   `json:"hue"`        // [0, 360) degrees from RuleID
	Intensity  float64   `json:"intensity"`  // [0.15, 1.0] from active capability popcount
	Complexity float64   `json:"complexity"` // [0.0, 1.0] linear saturating at SizeSaturation
}

var canonicalRuleHues = map[reduce.RuleID]float64{
	reduce.RuleI:   174.0, // Linear Identity Teal (#00E5C9)
	reduce.RuleK:   330.0, // Erasure (QTT 0) Rose (#FF6AC1)
	reduce.RuleS:   145.0, // Shared Substitution (QTT ω) Green (#00FF66)
	reduce.RuleB:   48.0,  // Linear Composition Gold (#FFD700)
	reduce.RuleC:   28.0,  // Linear Exchange Orange (#FA5B1C)
	reduce.RuleW:   275.0, // Duplication (QTT ω) Violet (#B388FF)
	reduce.RuleSc:  195.0, // Scope Constructor Cyan (#00F0FF)
	reduce.RuleAbs: 120.0, // Bracket Absorption Emerald (#00E676)
}

// MapStep deterministically maps a StepEvent onto a VisualState:
//   - Rule -> Hue in [0, 360): canonical Circle-Calculus rule lookup, or FNV-1a32 % 360
//   - PermMask -> Intensity in [0.15, 1.0]: scales with popcount(PermMask & PermAll)/4
//     so full root capability (W,K,C,S = 4 bits) blooms at 1.0 and empty mask = 0.15
//   - TermSize -> Complexity in [0.0, 1.0]: linear saturation at SizeSaturation (64)
func MapStep(ev StepEvent) VisualState {
	ruleKey := reduce.RuleID(strings.TrimSpace(string(ev.Rule)))
	if ruleKey == "" {
		ruleKey = reduce.RuleI
		ev.Rule = reduce.RuleI
	}

	hue, ok := canonicalRuleHues[ruleKey]
	if !ok {
		h := fnv.New32a()
		_, _ = h.Write([]byte(ruleKey))
		hue = float64(h.Sum32() % 360)
	}

	activeBits := bits.OnesCount8(uint8(ev.PermMask & reduce.PermAll))
	intensity := 0.15 + 0.85*(float64(activeBits)/4.0)
	intensity = math.Round(math.Max(0.15, math.Min(1.0, intensity))*10000) / 10000

	sz := ev.TermSize
	if sz < 0 {
		sz = 0
		ev.TermSize = 0
	}
	complexity := float64(sz) / float64(SizeSaturation)
	if complexity > 1.0 {
		complexity = 1.0
	}
	complexity = math.Round(complexity*10000) / 10000

	return VisualState{
		Event:      ev,
		Hue:        hue,
		Intensity:  intensity,
		Complexity: complexity,
	}
}

// Handler returns a pure-stdlib http.Handler serving:
//   - GET /        -> the embedded single-file interactive evaluator HTML
//   - GET /state   -> JSON of the current deterministic VisualState
//   - GET /events  -> text/event-stream SSE emitting the mapped VisualState frame
func Handler(embeddedHTML []byte, initial StepEvent) http.Handler {
	state := MapStep(initial)
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/index.html" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("X-Spherepop-Seed", "TERM-to-TRACE-to-LIGHT")
		_, _ = w.Write(embeddedHTML)
	})

	mux.HandleFunc("/state", func(w http.ResponseWriter, r *http.Request) {
		ev := initial
		if rl := r.URL.Query().Get("rule"); rl != "" {
			ev.Rule = reduce.RuleID(rl)
		}
		cur := MapStep(ev)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(cur)
	})

	mux.HandleFunc("/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		payload, _ := json.Marshal(state)
		_, _ = fmt.Fprintf(w, "event: spherepop\ndata: %s\n\n", payload)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	})

	return mux
}
