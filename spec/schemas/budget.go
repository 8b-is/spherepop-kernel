// Maintenance budget enforcement for Spherepop kernel
// Implements: kernel MUST track budget_consumed per maintenance_unit.
// On collapse, if budget_consumed + estimated_upkeep > budget_allocated,
// kernel MUST refuse with RefusalReason.BUDGET_EXCEEDED unless governance waiver exists.

package budget

import (
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrBudgetExceeded     = errors.New("budget: maintenance budget exceeded")
	ErrUnitNotFound       = errors.New("budget: maintenance unit not found")
	ErrInvalidBudget      = errors.New("budget: invalid budget configuration")
	ErrWaiverExpired      = errors.New("budget: governance waiver expired")
	ErrWaiverNotFound     = errors.New("budget: no valid governance waiver for this unit")
)

// Budget represents the maintenance budget for a project
type Budget struct {
	Project          string           `json:"project"`
	TotalBudget      int              `json:"total_budget"`
	Units            []MaintenanceUnit `json:"units"`
	UpdatedAt        time.Time        `json:"updated_at"`
	GovernanceWaivers []Waiver        `json:"governance_waivers"`
}

// MaintenanceUnit is a module/subsystem with its own budget allocation
type MaintenanceUnit struct {
	UnitID          string `json:"unit_id"`
	Name            string `json:"name"`
	Allocated       int    `json:"allocated"`
	Consumed        int    `json:"consumed"`
	EstimatedUpkeep int    `json:"estimated_upkeep"`
}

// Waiver is a governance-approved budget override
type Waiver struct {
	WaiverID   string    `json:"waiver_id"`
	UnitID     string    `json:"unit_id"`
	GrantedAt  time.Time `json:"granted_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	Authorizer string    `json:"authorizer"`
	Reason     string    `json:"reason"`
}

// CheckCollapse verifies if a sphere collapse would exceed the maintenance budget
// for the affected units. Returns ErrBudgetExceeded if so, unless a valid waiver exists.
func (b *Budget) CheckCollapse(affectedUnits map[string]int, now time.Time) error {
	for unitID, cost := range affectedUnits {
		unit, ok := b.findUnit(unitID)
		if !ok {
			return ErrUnitNotFound
		}

		projectedConsumed := unit.Consumed + cost
		projectedTotal := projectedConsumed + unit.EstimatedUpkeep

		if projectedTotal > unit.Allocated {
			// Check for valid governance waiver
			if !b.hasValidWaiver(unitID, now) {
				return ErrBudgetExceeded
			}
		}
	}
	return nil
}

// RecordCollapse updates the budget after a successful collapse
func (b *Budget) RecordCollapse(affectedUnits map[string]int) error {
	for unitID, cost := range affectedUnits {
		unit, ok := b.findUnit(unitID)
		if !ok {
			return ErrUnitNotFound
		}
		unit.Consumed += cost
	}
	b.UpdatedAt = time.Now().UTC()
	return nil
}

func (b *Budget) findUnit(unitID string) (*MaintenanceUnit, bool) {
	for i := range b.Units {
		if b.Units[i].UnitID == unitID {
			return &b.Units[i], true
		}
	}
	return nil, false
}

func (b *Budget) hasValidWaiver(unitID string, now time.Time) bool {
	for _, w := range b.GovernanceWaivers {
		if w.UnitID == unitID && now.After(w.GrantedAt) && now.Before(w.ExpiresAt) {
			return true
		}
	}
	return false
}

// ParseBudget parses a budget from JSON
func ParseBudget(data []byte) (*Budget, error) {
	var b Budget
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, err
	}
	if b.TotalBudget < 0 {
		return nil, ErrInvalidBudget
	}
	for _, u := range b.Units {
		if u.Allocated < 0 || u.Consumed < 0 || u.EstimatedUpkeep < 0 {
			return nil, ErrInvalidBudget
		}
		if u.Consumed > u.Allocated {
			return nil, ErrInvalidBudget
		}
	}
	return &b, nil
}