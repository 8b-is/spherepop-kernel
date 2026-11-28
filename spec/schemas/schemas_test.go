package schemas

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/standardgalactic/spherepop-kernel/term"
)

func TestRatioCanonicalAndBonoboResonance(t *testing.T) {
	r, err := NewRatio(6, 4)
	if err != nil {
		t.Fatalf("NewRatio: %v", err)
	}
	if r != (Ratio{P: 3, Q: 2}) {
		t.Fatalf("got %v, want 3/2", r)
	}
	if !r.IsBonoboResonance() {
		t.Fatal("expected 3/2 to report Rung 0 Bonobo resonance")
	}
	if _, err := NewRatio(1, 0); !errors.Is(err, ErrZeroDenominator) {
		t.Fatalf("expected ErrZeroDenominator, got %v", err)
	}
	if (Ratio{P: 2, Q: 3}).Cmp(Ratio{P: 67, Q: 100}) >= 0 {
		t.Fatal("expected 2/3 < 67/100")
	}
}

func TestBudgetEnforcementAndWaivers(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	bJSON := `{
		"project": "cid:abc123",
		"total_budget": 100,
		"units": [
			{"unit_id": "core-kernel", "name": "core/kernel", "allocated": 50, "consumed": 30, "estimated_upkeep": 15}
		],
		"updated_at": "2026-11-26T00:00:00Z",
		"governance_waivers": [
			{
				"waiver_id": "cid:waiver1",
				"unit_id": "core-kernel",
				"granted_at": "2026-11-26T00:00:00Z",
				"expires_at": "2026-11-26T06:00:00Z",
				"authorizer": "did:key:z6MkhaXgBZDvotDkL5257faiztiGiC2QtKLGpbnnEGta2doK"
			}
		]
	}`
	b, err := ParseBudget([]byte(bJSON))
	if err != nil {
		t.Fatalf("ParseBudget: %v", err)
	}
	// Exact fit: 30 + 5 + 15 = 50 <= 50
	if err := b.CheckCollapse(map[string]int{"core-kernel": 5}, now); err != nil {
		t.Fatalf("expected exact fit to pass, got %v", err)
	}
	// Overflow with expired waiver at 12:00Z -> ErrBudgetExceeded + ErrWaiverExpired
	err = b.CheckCollapse(map[string]int{"core-kernel": 6}, now)
	if !errors.Is(err, ErrBudgetExceeded) || !errors.Is(err, ErrWaiverExpired) {
		t.Fatalf("expected ErrBudgetExceeded + ErrWaiverExpired, got %v", err)
	}
	// Overflow during active waiver window at 03:00Z -> permitted
	if err := b.CheckCollapse(map[string]int{"core-kernel": 6}, time.Date(2026, 11, 26, 3, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("expected active waiver to permit collapse, got %v", err)
	}
	// Negative cost must fail closed
	if err := b.CheckCollapse(map[string]int{"core-kernel": -10}, now); !errors.Is(err, ErrInvalidBudget) {
		t.Fatalf("expected ErrInvalidBudget on negative cost, got %v", err)
	}
	if err := b.RecordCollapse(map[string]int{"core-kernel": 5}); err != nil {
		t.Fatalf("RecordCollapse: %v", err)
	}
	// Utilization: (35 + 15) / 50 = 1/1
	if got := b.UtilizationRatio(); got != (Ratio{P: 1, Q: 1}) {
		t.Fatalf("UtilizationRatio = %v, want 1/1", got)
	}
}

func TestFootprintSubsumption(t *testing.T) {
	cur := &Footprint{
		FootprintID:         "cid:cur",
		Arch:                "aarch64",
		OS:                  "darwin",
		Libc:                "libsystem",
		RuntimeVersions:     map[string]string{"go": "1.22.4", "zig": "0.16.2"},
		Accelerator:         []string{"metal"},
		EnvironmentManifest: "cid:man1",
	}
	evValid := &Footprint{
		FootprintID:         "cid:ev1",
		Arch:                "aarch64",
		OS:                  "darwin",
		Libc:                "libsystem",
		RuntimeVersions:     map[string]string{"go": "1.22.1", "zig": "0.16.0"},
		Accelerator:         []string{"metal"},
		EnvironmentManifest: "cid:man1",
	}
	if err := cur.Subsumes(evValid); err != nil {
		t.Fatalf("expected valid subsumption, got %v", err)
	}

	// Patch higher in evidence (1.22.9 > 1.22.4) must be rejected
	evNewerPatch := *evValid
	evNewerPatch.RuntimeVersions = map[string]string{"go": "1.22.9"}
	if err := cur.Subsumes(&evNewerPatch); !errors.Is(err, ErrRuntimeVersionMismatch) {
		t.Fatalf("expected ErrRuntimeVersionMismatch on newer patch, got %v", err)
	}

	// Missing required runtime in current must be rejected
	evMissingRuntime := *evValid
	evMissingRuntime.RuntimeVersions = map[string]string{"rustc": "1.78.0"}
	if err := cur.Subsumes(&evMissingRuntime); !errors.Is(err, ErrRuntimeVersionMismatch) {
		t.Fatalf("expected ErrRuntimeVersionMismatch on missing runtime, got %v", err)
	}

	// 0.x breaking minor mismatch (0.15.0 vs 0.16.2) must be rejected
	evZeroMinor := *evValid
	evZeroMinor.RuntimeVersions = map[string]string{"zig": "0.15.0"}
	if err := cur.Subsumes(&evZeroMinor); !errors.Is(err, ErrRuntimeVersionMismatch) {
		t.Fatalf("expected ErrRuntimeVersionMismatch on 0.x minor mismatch, got %v", err)
	}

	hostFP := CurrentFootprint()
	if !strings.HasPrefix(hostFP.FootprintID, "cid:") {
		t.Fatalf("invalid host FootprintID %q", hostFP.FootprintID)
	}
}

func TestGovernanceQuorumVetoEmergencyAndPatch(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	d1 := "did:key:z6Mk1"
	d2 := "did:key:z6Mk2"
	d3 := "did:key:z6Mk3"

	p := &Proposal{
		ProposalID:  "cid:prop1",
		Rationale:   "Raise max fuel cap",
		QuorumRatio: Ratio{P: 2, Q: 3},
		VetoHolders: []string{d1},
		Emergency:   true,
		Status:      "pending",
		Authorizers: []string{d1, d2, d3},
		Patch: PatchDocument{
			Operations: []PatchOperation{
				{Op: "test", Path: "/max_fuel", Value: json.RawMessage(`1000`)},
				{Op: "replace", Path: "/max_fuel", Value: json.RawMessage(`2000`)},
				{Op: "add", Path: "/audit_mode", Value: json.RawMessage(`"strict"`)},
			},
		},
	}

	// Emergency without veto-holder explicit accept must fail even if 2/3 others accept
	_ = p.Vote(d2, "accept")
	_ = p.Vote(d3, "accept")
	if err := p.Resolve(now); !errors.Is(err, ErrVetoBlocked) {
		t.Fatalf("expected ErrVetoBlocked when veto holder has not accepted emergency, got %v", err)
	}

	// Reset and pass with veto holder d1 + d2 (2/3 >= 1/2 emergency quorum)
	p.Status = "pending"
	p.ResolvedAt = nil
	p.Votes = nil
	_ = p.Vote(d1, "accept")
	_ = p.Vote(d2, "accept")
	if err := p.Resolve(now); err != nil {
		t.Fatalf("expected emergency proposal accepted, got %v", err)
	}

	updated, ev, err := p.ApplyPatch(json.RawMessage(`{"max_fuel":1000}`), d1)
	if err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}
	if !strings.Contains(string(updated), `"max_fuel":2000`) || !strings.Contains(string(updated), `"audit_mode":"strict"`) {
		t.Fatalf("unexpected patched policy: %s", string(updated))
	}
	if !strings.HasPrefix(ev.EventID, "cid:") || len(ev.PatchHash) != 64 {
		t.Fatalf("invalid GovernanceEvent: %+v", ev)
	}
}

func TestPercolationLODAndUnitaryZeroFloat(t *testing.T) {
	// Term shadow graph extraction
	tm := term.MustParse("S a (b c)")
	sg, err := BuildShadowGraphFromTerm(tm, map[string]string{
		"a": "pkg/alpha",
		"b": "pkg/beta",
		"c": "pkg/gamma",
	})
	if err != nil {
		t.Fatalf("BuildShadowGraphFromTerm: %v", err)
	}
	// K3 triangle: degrees (2,2,2), sumK=6, sumK2=12 -> kappa = 2/1 (critical)
	if sg.KappaRatio != (Ratio{P: 2, Q: 1}) || sg.Regime != "critical" {
		t.Fatalf("expected K3 critical kappa=2/1, got %v (%s)", sg.KappaRatio, sg.Regime)
	}
	rawSG, err := sg.ToJSON()
	if err != nil || len(rawSG) == 0 {
		t.Fatalf("ShadowGraph.ToJSON failed: %v", err)
	}

	// LOD determinism check (run twice, verify identical JSON modulo timestamp)
	adj := [][]int{{1, 2}, {0, 2}, {0, 1}, {4}, {3}}
	p1, err := SzemerediPartition(adj, []string{"a", "b", "c", "d", "e"}, Ratio{P: 1, Q: 10})
	if err != nil {
		t.Fatalf("SzemerediPartition: %v", err)
	}
	p2, err := SzemerediPartition(adj, []string{"a", "b", "c", "d", "e"}, Ratio{P: 1, Q: 10})
	if err != nil {
		t.Fatalf("SzemerediPartition 2: %v", err)
	}
	if p1.PartitionID != p2.PartitionID || p1.SteinerHub != p2.SteinerHub {
		t.Fatalf("non-deterministic SzemerediPartition CIDs: %s vs %s", p1.PartitionID, p2.PartitionID)
	}

	// WaveFunction non-uniform radial packet & bounded norm drift
	psiBefore := InitializeWaveFunction()
	centerAmp := psiBefore.Data[(32*GridSize+32)*2]
	cornerAmp := psiBefore.Data[0]
	if centerAmp != Q16_ONE || cornerAmp >= centerAmp {
		t.Fatalf("expected radial wave packet decay: center=%d corner=%d", centerAmp, cornerAmp)
	}
	psiAfter := *psiBefore
	vks := BuildKohnShamPotential(&psiAfter)
	StrangFullStep(&psiAfter, vks, 512) // dt = 1/128
	drift := VerifyUnitarity(psiBefore, &psiAfter)
	if drift > Q16_ONE {
		t.Fatalf("excessive norm drift: %d", drift)
	}
}
