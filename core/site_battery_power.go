package core

import (
	"time"

	"github.com/evcc-io/evcc/api"
	"github.com/evcc-io/evcc/tariff"
	"github.com/evcc-io/evcc/util/grid"
)

const (
	// below this charge power the battery is released to normal instead of being held idle
	batteryMinChargePower = 100.0 // W

	// shortcut: fixed time the battery needs to follow the slot budget, as for the grid peak in evcc-io/evcc#35251
	gridImportLag = time.Minute
)

// batteryPowerControlled reports whether a controllable battery accepts a power setpoint
func (site *Site) batteryPowerControlled() bool {
	for _, dev := range site.batteryMeters {
		meter := dev.Instance()
		if api.HasCap[api.BatteryController](meter) && api.HasCap[api.BatteryPowerController](meter) {
			return true
		}
	}
	return false
}

// gridImportBudget returns the grid import allowed for the rest of the running slot so that the slot
// average stays at the grid import limit, capped at the limit. False without limit or slot energy.
func (site *Site) gridImportBudget() (float64, bool) {
	limit := site.GetGridImportLimit()
	if limit <= 0 {
		return 0, false
	}

	c, ok := site.collectors[site.Meters.GridMeterRef]
	if !ok {
		return limit, true
	}

	energy, elapsed, ok := c.SlotEnergy()
	if !ok {
		return limit, true
	}

	// floor the time left, the battery needs it to follow
	remaining := max(tariff.SlotDuration-elapsed, gridImportLag)
	budget := (limit*tariff.SlotDuration.Hours() - energy*1e3) / remaining.Hours()

	// the budget only lowers the limit
	return min(limit, max(budget, 0)), true
}

// batteryChargeHeadroom returns the grid charge power the circuit and the slot budget allow,
// including what the batteries draw now. Charging counts as load on the circuit meter, so the
// current draw is passed as the old value and handed back if the limits still allow it.
func (site *Site) batteryChargeHeadroom() float64 {
	res := site.batteryMaxChargePower()
	if res <= 0 {
		return 0
	}

	state := site.state()
	charging := max(0, -state.battery.Power)

	if c := site.circuit; c != nil {
		// a meterless circuit sums loadpoints only, the battery draw is not part of it
		old := charging
		if !c.HasMeter() {
			old = 0
		}

		res = c.ValidatePower(old, res)

		// shortcut: three-phase battery charging, as in the start gate
		current := c.ValidateCurrent(grid.PowerToCurrent(old, 3), grid.PowerToCurrent(res, 3))
		res = min(res, grid.CurrentToPower(current, 3))
	}

	// the grid power includes the battery's own draw
	if budget, ok := site.gridImportBudget(); ok {
		res = min(res, budget-(state.gridPower-charging))
	}

	return max(0, res)
}

// batteryChargeShares splits the charge setpoint over the power controlled batteries by their charge limits
func (site *Site) batteryChargeShares(total float64) map[string]float64 {
	limits := make(map[string]float64)
	var sum float64

	for _, dev := range site.batteryMeters {
		meter := dev.Instance()
		if !api.HasCap[api.BatteryController](meter) || !api.HasCap[api.BatteryPowerController](meter) {
			continue
		}

		var limit float64
		if bpl, ok := api.Cap[api.BatteryPowerLimiter](meter); ok {
			if charge, _, err := bpl.GetPowerLimits(); err == nil {
				limit = charge
			}
		}

		limits[dev.Config().Name] = limit
		sum += limit
	}

	res := make(map[string]float64, len(limits))
	for name, limit := range limits {
		if sum > 0 {
			res[name] = total * limit / sum
		} else {
			res[name] = total / float64(len(limits))
		}
	}

	return res
}
