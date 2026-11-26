package core

import (
	"math"
	"time"

	"github.com/evcc-io/evcc/api"
	"github.com/evcc-io/evcc/tariff"
	"github.com/evcc-io/evcc/util/grid"
)

const (
	// shortcut: fixed time the battery needs to follow the slot budget, as for the grid peak in evcc-io/evcc#35251
	gridImportLag = time.Minute

	// a setpoint is written again when it changes by this much or when it gets this old.
	// The refresh stays well inside the device fallback (Huawei forced period: 2 minutes).
	batteryPowerDeadband = 50.0 // W
	batteryPowerRefresh  = time.Minute
)

// batteryPowerSetpoint is the last setpoint written to a battery
type batteryPowerSetpoint struct {
	power float64 // W, positive discharges
	at    time.Time
}

// powerBatteries returns the controllable batteries with a power setpoint and their summed power limits
func (site *Site) powerBatteries() (names []string, charge, discharge float64) {
	for _, dev := range site.batteryMeters {
		meter := dev.Instance()
		if !api.HasCap[api.BatteryController](meter) || !api.HasCap[api.BatteryPowerController](meter) {
			continue
		}

		names = append(names, dev.Config().Name)

		if bpl, ok := api.Cap[api.BatteryPowerLimiter](meter); ok {
			if c, d, err := bpl.GetPowerLimits(); err == nil {
				charge += c
				discharge += d
			}
		}
	}

	return names, charge, discharge
}

// importBudget returns the grid import power allowed for the rest of the slot so that the slot average
// stays at the limit, never above the limit (evcc-io/evcc#35251). Energy is the slot's import in kWh.
func importBudget(limit, energy float64, elapsed time.Duration) float64 {
	// floor the time left, the battery needs it to follow
	remaining := max(tariff.SlotDuration-elapsed, gridImportLag)
	budget := (limit*tariff.SlotDuration.Hours() - energy*1e3) / remaining.Hours()

	// the budget only lowers the limit
	return min(limit, budget)
}

// gridImportBudget returns the grid import allowed now by the grid import limit, false without limit
func (site *Site) gridImportBudget() (float64, bool) {
	limit := site.GetGridImportLimit()
	if limit <= 0 {
		return 0, false
	}

	if c, ok := site.collectors[site.Meters.GridMeterRef]; ok {
		if energy, elapsed, ok := c.SlotEnergy(); ok {
			return importBudget(limit, energy, elapsed), true
		}
	}

	return limit, true
}

// blindValue reports a circuit measurement the circuit has given up on (meter timeout)
func blindValue(v float64) bool {
	return v >= math.MaxFloat64/2
}

// batteryChargeTarget returns the signed grid charge power of the power controlled batteries (positive charges):
// the charge limit, reduced to the headroom of the root circuit and the slot's import budget, down to discharging
// as far as an overloaded circuit or an exceeded budget needs. False when a limit cannot be measured.
func (site *Site) batteryChargeTarget(chargeLimit, dischargeLimit float64) (float64, bool) {
	res := chargeLimit
	state := site.state()

	// the battery's own draw is part of the measured grid and circuit power
	charging := -state.battery.Power

	if c := site.circuit; c != nil {
		power, current := c.GetChargePower(), c.GetMaxPhaseCurrent()
		if blindValue(power) || blindValue(current) {
			return 0, false
		}

		// a meterless circuit sums loadpoints only, the battery draw is not part of it
		own := charging
		if !c.HasMeter() {
			own = 0
		}

		if maxPower := c.EffectiveMaxPower(); maxPower > 0 {
			res = min(res, maxPower-power+own)
		}

		// shortcut: three-phase battery, as in the start gate of #34401
		if maxCurrent := c.GetMaxCurrent(); maxCurrent > 0 {
			res = min(res, grid.CurrentToPower(maxCurrent-current, 3)+own)
		}
	}

	if budget, ok := site.gridImportBudget(); ok {
		res = min(res, budget-state.gridPower+charging)
	}

	return max(res, -dischargeLimit), true
}

// batteryPowerShares splits the signed setpoint (positive charges) evenly over the power controlled batteries
func batteryPowerShares(names []string, total float64) map[string]float64 {
	res := make(map[string]float64, len(names))
	for _, name := range names {
		res[name] = total / float64(len(names))
	}
	return res
}

// writeBatteryPower writes a battery's setpoint (positive discharges) when it changed or got old
func (site *Site) writeBatteryPower(name string, bpc api.BatteryPowerController, power float64, now time.Time) (bool, error) {
	if site.batteryPowerWritten == nil {
		site.batteryPowerWritten = make(map[string]batteryPowerSetpoint)
	}

	if last, ok := site.batteryPowerWritten[name]; ok && math.Abs(last.power-power) < batteryPowerDeadband && now.Sub(last.at) < batteryPowerRefresh {
		return false, nil
	}

	if err := bpc.SetBatteryPower(power); err != nil {
		return false, err
	}

	site.batteryPowerWritten[name] = batteryPowerSetpoint{power: power, at: now}
	return true, nil
}
