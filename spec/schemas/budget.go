// Maintenance budget enforcement for Spherepop kernel (Protocol §12).
// Tracks consumed and ongoing upkeep units per maintenance_unit using exact
// integer accounting and reduced rational p/q utilization metrics.

package schemas

import (
	"encoding/json"
	"errors"
	"sort"
	"time"
)

var (
	ErrBudgetExceeded = errors.New("budget: maintenance budget exceeded")
	ErrUnitNotFound   = errors.New("budget: maintenance unit not found")
	ErrInvalidBudget  = errors.New("budget: invalid budget configuration")
	ErrWaiverExpired  = errors.New("budget: governance waiver expired")
	ErrWaiverNotFound = errors.New("budget: no valid governance waiver for this unit")
)

// Budget represents the maintenance budget for a project.
type Budget struct {
	Project           string            `json:"project"`
	TotalBudget       int               `json:"total_budget"`
	Units             []MaintenanceUnit `json:"units"`
	Utilization       *Ratio            `json:"utilization_ratio,omitempty"`
	UpdatedAt         time.Time         `json:"updated_at"`
	GovernanceWaivers []Waiver          `json:"governance_waivers,omitempty"`
}

// MaintenanceUnit is a module/subsystem with its own budget allocation.
type MaintenanceUnit struct {
	UnitID          string `json:"unit_id"`
	Name            string `json:"name"`
	Allocated       int    `json:"allocated"`
	Consumed        int    `json:"consumed"`
	EstimatedUpkeep int    `json:"estimated_upkeep"`
}

// Waiver is a governance-approved budget override.
type Waiver struct {
	WaiverID   string    `json:"waiver_id"`
	UnitID     string    `json:"unit_id"`
	GrantedAt  time.Time `json:"granted_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	Authorizer string    `json:"authorizer"`
	Reason     string    `json:"reason,omitempty"`
}

// CheckCollapse verifies whether a sphere collapse would exceed the maintenance
// budget for any affected unit. Costs must be non-negative.
func (b *Budget) CheckCollapse(affectedUnits map[string]int, now time.Time) error {
	if b == nil {
		return ErrInvalidBudget
	}
	unitIDs := make([]string, 0, len(affectedUnits))
	for id := range affectedUnits {
		unitIDs = append(unitIDs, id)
	}
	sort.Strings(unitIDs)

	for _, unitID := range unitIDs {
		cost := affectedUnits[unitID]
		if cost < 0 {
			return ErrInvalidBudget
		}
		unit, ok := b.findUnit(unitID)
		if !ok {
			return ErrUnitNotFound
		}
		projectedTotal := unit.Consumed + cost + unit.EstimatedUpkeep
		if projectedTotal > unit.Allocated {
			if err := b.CheckWaiver(unitID, now); err != nil {
				return errors.Join(ErrBudgetExceeded, err)
			}
		}
	}
	return nil
}

// CheckWaiver verifies whether a valid, non-expired governance waiver covers unitID at now.
func (b *Budget) CheckWaiver(unitID string, now time.Time) error {
	sawExpired := false
	for _, w := range b.GovernanceWaivers {
		if w.UnitID != unitID {
			continue
		}
		if !now.Before(w.GrantedAt) && now.Before(w.ExpiresAt) {
			return nil
		}
		if !now.Before(w.ExpiresAt) {
			sawExpired = true
		}
	}
	if sawExpired {
		return ErrWaiverExpired
	}
	return ErrWaiverNotFound
}

// RecordCollapse atomically updates unit consumption after validating all unit IDs and costs.
func (b *Budget) RecordCollapse(affectedUnits map[string]int) error {
	if b == nil {
		return ErrInvalidBudget
	}
	for unitID, cost := range affectedUnits {
		if cost < 0 {
			return ErrInvalidBudget
		}
		if _, ok := b.findUnit(unitID); !ok {
			return ErrUnitNotFound
		}
	}
	for unitID, cost := range affectedUnits {
		unit, _ := b.findUnit(unitID)
		unit.Consumed += cost
	}
	u := b.UtilizationRatio()
	b.Utilization = &u
	b.UpdatedAt = time.Now().UTC()
	return nil
}

// UtilizationRatio computes the canonical reduced rational p/q of
// sum(consumed + estimated_upkeep) / sum(allocated) across all units.
func (b *Budget) UtilizationRatio() Ratio {
	if b == nil || len(b.Units) == 0 {
		return Ratio{P: 0, Q: 1}
	}
	var num, den uint64
	for _, u := range b.Units {
		if u.Consumed > 0 {
			num += uint64(u.Consumed)
		}
		if u.EstimatedUpkeep > 0 {
			num += uint64(u.EstimatedUpkeep)
		}
		if u.Allocated > 0 {
			den += uint64(u.Allocated)
		}
	}
	if den == 0 {
		if b.TotalBudget > 0 {
			return MustRatio(num, uint64(b.TotalBudget))
		}
		return Ratio{P: 0, Q: 1}
	}
	return MustRatio(num, den)
}

func (b *Budget) findUnit(unitID string) (*MaintenanceUnit, bool) {
	for i := range b.Units {
		if b.Units[i].UnitID == unitID {
			return &b.Units[i], true
		}
	}
	return nil, false
}

// ParseBudget parses and validates a Budget JSON document.
func ParseBudget(data []byte) (*Budget, error) {
	var b Budget
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, err
	}
	if b.Project == "" || b.TotalBudget < 0 {
		return nil, ErrInvalidBudget
	}
	seen := make(map[string]bool, len(b.Units))
	sumAllocated := 0
	for _, u := range b.Units {
		if u.UnitID == "" || seen[u.UnitID] {
			return nil, ErrInvalidBudget
		}
		seen[u.UnitID] = true
		if u.Allocated < 0 || u.Consumed < 0 || u.EstimatedUpkeep < 0 {
			return nil, ErrInvalidBudget
		}
		if u.Consumed > u.Allocated {
			return nil, ErrInvalidBudget
		}
		sumAllocated += u.Allocated
	}
	if sumAllocated > b.TotalBudget {
		return nil, ErrInvalidBudget
	}
	ratio := b.UtilizationRatio()
	b.Utilization = &ratio
	return &b, nil
}
