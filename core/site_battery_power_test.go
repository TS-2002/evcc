package core

import (
	"math"
	"testing"
	"time"

	"github.com/evcc-io/evcc/api"
	"github.com/evcc-io/evcc/core/types"
	"github.com/evcc-io/evcc/util"
	"github.com/evcc-io/evcc/util/config"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
)

// powerBattery is a battery meter with mode control, power setpoint and power limits
func powerBattery(ctrl *gomock.Controller) (api.Meter, *api.MockBatteryController, *api.MockBatteryPowerController) {
	batCon := batteryControllerMock(ctrl)
	batPow := api.NewMockBatteryPowerController(ctrl)

	return &struct {
		api.Meter
		api.BatteryController
		api.BatteryPowerController
		api.BatteryPowerLimiter
	}{
		BatteryController:      batCon,
		BatteryPowerController: batPow,
		BatteryPowerLimiter:    &mockBatteryPowerLimiter{charge: 5000, discharge: 3000},
	}, batCon, batPow
}

// metered root circuit with a power limit of 10 kW
func powerCircuit(ctrl *gomock.Controller, power float64) *api.MockCircuit {
	c := api.NewMockCircuit(ctrl)
	c.EXPECT().HasMeter().Return(true).AnyTimes()
	c.EXPECT().EffectiveMaxPower().Return(10000.0).AnyTimes()
	c.EXPECT().GetChargePower().Return(power).AnyTimes()
	c.EXPECT().GetMaxCurrent().Return(0.0).AnyTimes()
	c.EXPECT().GetMaxPhaseCurrent().Return(0.0).AnyTimes()
	return c
}

// TestBatteryGridChargePower guards that a power controlled battery takes exactly the headroom of the circuit
// and the slot's import budget, down to 0 W and to discharging, and is released to normal only when blind
func TestBatteryGridChargePower(t *testing.T) {
	for _, tc := range []struct {
		name         string
		circuitPower float64 // incl. the battery's 3 kW draw, 0 = no circuit
		importLimit  float64 // 0 = none
		gridPower    float64
		invalid      bool    // meters failed this cycle
		expect       float64 // setpoint written (positive discharges), NaN = released to normal
	}{
		{"full charge limit", 8000, 0, 8000, false, -5000},
		{"circuit headroom", 9000, 0, 9000, false, -4000},
		{"no headroom holds 0 W", 13000, 0, 13000, false, 0},
		{"overloaded circuit discharges", 15000, 0, 15000, false, 2000},
		{"discharge limited", 30000, 0, 30000, false, 3000},
		{"import limit without circuit", 0, 5000, 6000, false, -2000},
		{"circuit tighter than import limit", 9500, 8000, 6000, false, -3500},
		{"blind circuit releases", math.MaxFloat64, 0, 0, false, math.NaN()},
		{"meter error releases", 8000, 0, 8000, true, math.NaN()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)

			bat, batCon, batPow := powerBattery(ctrl)

			site := &Site{
				log:             util.NewLogger("foo"),
				batteryMeters:   []config.Device[api.Meter]{config.NewStaticDevice(config.Named{Name: "bat"}, bat)},
				batteryMode:     api.BatteryNormal,
				gridImportLimit: tc.importLimit,
				metersValid:     !tc.invalid,
				siteState:       siteState{gridPower: tc.gridPower, battery: types.BatteryState{Power: -3000}},
			}

			if tc.circuitPower != 0 {
				site.circuit = powerCircuit(ctrl, tc.circuitPower)
			}

			// entering power control goes through normal once, releasing as well
			batCon.EXPECT().SetBatteryMode(api.BatteryNormal)
			if !math.IsNaN(tc.expect) {
				batPow.EXPECT().SetBatteryPower(tc.expect)
			}

			site.updateBatteryMode(true, false, api.Rate{})

			if math.IsNaN(tc.expect) {
				assert.Equal(t, api.BatteryNormal, site.GetBatteryMode())
				assert.Nil(t, site.batteryPowerTarget)
			} else {
				assert.Equal(t, api.BatteryCharge, site.GetBatteryMode())
			}

			ctrl.Finish()
		})
	}
}

// TestBatteryGridChargePowerWrites guards that the setpoint is written on change and refreshed before the
// device fallback expires, not every cycle
func TestBatteryGridChargePowerWrites(t *testing.T) {
	ctrl := gomock.NewController(t)

	bat, batCon, batPow := powerBattery(ctrl)
	batCon.EXPECT().SetBatteryMode(api.BatteryNormal).Times(1)

	circuitPower := 8000.0
	c := api.NewMockCircuit(ctrl)
	c.EXPECT().HasMeter().Return(true).AnyTimes()
	c.EXPECT().EffectiveMaxPower().Return(10000.0).AnyTimes()
	c.EXPECT().GetChargePower().DoAndReturn(func() float64 { return circuitPower }).AnyTimes()
	c.EXPECT().GetMaxCurrent().Return(0.0).AnyTimes()
	c.EXPECT().GetMaxPhaseCurrent().Return(0.0).AnyTimes()

	site := &Site{
		log:           util.NewLogger("foo"),
		batteryMeters: []config.Device[api.Meter]{config.NewStaticDevice(config.Named{Name: "bat"}, bat)},
		batteryMode:   api.BatteryNormal,
		circuit:       c,
		metersValid:   true,
		siteState:     siteState{battery: types.BatteryState{Power: -3000}},
	}

	// unchanged setpoint: written once
	batPow.EXPECT().SetBatteryPower(-5000.0).Times(1)
	for range 3 {
		site.updateBatteryMode(true, false, api.Rate{})
	}

	// change below the deadband: not written
	circuitPower = 8030 // headroom 4970
	site.updateBatteryMode(true, false, api.Rate{})

	// change above the deadband: written
	circuitPower = 11000 // headroom 2000
	batPow.EXPECT().SetBatteryPower(-2000.0).Times(1)
	site.updateBatteryMode(true, false, api.Rate{})

	// old setpoint: refreshed
	w := site.batteryPowerWritten["bat"]
	w.at = w.at.Add(-batteryPowerRefresh)
	site.batteryPowerWritten["bat"] = w
	batPow.EXPECT().SetBatteryPower(-2000.0).Times(1)
	site.updateBatteryMode(true, false, api.Rate{})

	ctrl.Finish()
}

// TestImportBudget guards the slot budget of evcc-io/evcc#35251: catch up below the limit, pay back above it
func TestImportBudget(t *testing.T) {
	for _, tc := range []struct {
		name    string
		energy  float64 // kWh imported in the slot
		elapsed time.Duration
		expect  float64
	}{
		{"never above the limit", 0, 5 * time.Minute, 5000},
		{"over-imported slot pays back", 0.5, 5 * time.Minute, 4500},
		{"exhausted slot asks for export", 1.3, 14*time.Minute + 59*time.Second, -3000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.InDelta(t, tc.expect, importBudget(5000, tc.energy, tc.elapsed), 1e-6)
		})
	}
}

// TestBatteryPowerShares guards the even split over several power controlled batteries
func TestBatteryPowerShares(t *testing.T) {
	assert.Equal(t, map[string]float64{"a": 2000, "b": 2000}, batteryPowerShares([]string{"a", "b"}, 4000))
}

// TestBatteryGridChargePowerReadFailed guards that a failed battery power read (reported as 0 W) releases to normal
func TestBatteryGridChargePowerReadFailed(t *testing.T) {
	ctrl := gomock.NewController(t)

	bat, batCon, _ := powerBattery(ctrl)
	batCon.EXPECT().SetBatteryMode(api.BatteryNormal)

	site := &Site{
		log:           util.NewLogger("foo"),
		batteryMeters: []config.Device[api.Meter]{config.NewStaticDevice(config.Named{Name: "bat"}, bat)},
		batteryMode:   api.BatteryNormal,
		circuit:       powerCircuit(ctrl, 8000),
		metersValid:   true,
		siteState:     siteState{batteryFailed: true},
	}

	site.updateBatteryMode(true, false, api.Rate{})
	assert.Equal(t, api.BatteryNormal, site.GetBatteryMode())

	ctrl.Finish()
}

// TestBatteryGridChargePowerRiseSlow guards that the setpoint falls at once but rises only when two cycles agree,
// so measurement noise between grid and battery reads never adds headroom
func TestBatteryGridChargePowerRiseSlow(t *testing.T) {
	ctrl := gomock.NewController(t)

	bat, batCon, batPow := powerBattery(ctrl)
	batCon.EXPECT().SetBatteryMode(api.BatteryNormal)

	circuitPower := 8000.0 // headroom 5000 with the battery's 3 kW
	c := api.NewMockCircuit(ctrl)
	c.EXPECT().HasMeter().Return(true).AnyTimes()
	c.EXPECT().EffectiveMaxPower().Return(10000.0).AnyTimes()
	c.EXPECT().GetChargePower().DoAndReturn(func() float64 { return circuitPower }).AnyTimes()
	c.EXPECT().GetMaxCurrent().Return(0.0).AnyTimes()
	c.EXPECT().GetMaxPhaseCurrent().Return(0.0).AnyTimes()

	site := &Site{
		log:           util.NewLogger("foo"),
		batteryMeters: []config.Device[api.Meter]{config.NewStaticDevice(config.Named{Name: "bat"}, bat)},
		batteryMode:   api.BatteryNormal,
		circuit:       c,
		metersValid:   true,
		siteState:     siteState{battery: types.BatteryState{Power: -3000}},
	}

	gomock.InOrder(
		batPow.EXPECT().SetBatteryPower(-5000.0),
		batPow.EXPECT().SetBatteryPower(-2000.0),
		batPow.EXPECT().SetBatteryPower(-5000.0),
	)

	site.updateBatteryMode(true, false, api.Rate{}) // 5000

	circuitPower = 11000 // headroom 2000: falls at once
	site.updateBatteryMode(true, false, api.Rate{})

	circuitPower = 8000 // headroom 5000 once: stays at 2000
	site.updateBatteryMode(true, false, api.Rate{})

	// headroom 5000 again: rises
	site.updateBatteryMode(true, false, api.Rate{})

	ctrl.Finish()
}
