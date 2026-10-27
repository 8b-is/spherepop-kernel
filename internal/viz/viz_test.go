package viz

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/standardgalactic/spherepop-kernel/reduce"
)

func TestMapStepDeterministic(t *testing.T) {
	ev := StepEvent{
		Rule:       reduce.RuleS,
		PermMask:   reduce.PermAll,
		TermSize:   16,
		TritLength: 20,
	}
	st := MapStep(ev)
	if st.Hue != 145.0 {
		t.Fatalf("Hue = %v, want 145.0", st.Hue)
	}
	if st.Intensity != 1.0 {
		t.Fatalf("Intensity = %v, want 1.0", st.Intensity)
	}
	if st.Complexity != 0.25 {
		t.Fatalf("Complexity = %v, want 0.25", st.Complexity)
	}

	// Empty permission mask -> minimum intensity 0.15; saturated size -> 1.0.
	stMin := MapStep(StepEvent{Rule: reduce.RuleK, PermMask: 0, TermSize: 200})
	if stMin.Hue != 330.0 || stMin.Intensity != 0.15 || stMin.Complexity != 1.0 {
		t.Fatalf("unexpected stMin: %+v", stMin)
	}
}

func TestHandlerRoutesAndSSE(t *testing.T) {
	h := Handler([]byte("<html>spherepop</html>"), StepEvent{
		Rule:     reduce.RuleSc,
		PermMask: reduce.PermW | reduce.PermK,
		TermSize: 32,
	})

	// 1. GET /
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK || rec.Header().Get("X-Spherepop-Seed") != "TERM-to-TRACE-to-LIGHT" {
		t.Fatalf("GET / failed: code=%d headers=%v", rec.Code, rec.Header())
	}

	// 2. GET /state
	recState := httptest.NewRecorder()
	h.ServeHTTP(recState, httptest.NewRequest(http.MethodGet, "/state?rule=Abs", nil))
	var vs VisualState
	if err := json.Unmarshal(recState.Body.Bytes(), &vs); err != nil {
		t.Fatalf("Unmarshal /state: %v", err)
	}
	if vs.Hue != 120.0 || vs.Complexity != 0.5 {
		t.Fatalf("unexpected /state: %+v", vs)
	}

	// 3. GET /events SSE
	recSSE := httptest.NewRecorder()
	h.ServeHTTP(recSSE, httptest.NewRequest(http.MethodGet, "/events", nil))
	if !strings.Contains(recSSE.Body.String(), "event: spherepop\ndata: ") {
		t.Fatalf("unexpected SSE body: %q", recSSE.Body.String())
	}
}
