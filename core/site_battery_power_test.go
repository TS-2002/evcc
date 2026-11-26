package core

import (
	"testing"

	"github.com/evcc-io/evcc/api"
	"github.com/evcc-io/evcc/core/types"
	"github.com/evcc-io/evcc/util"
	"github.com/evcc-io/evcc/util/config"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
)

// powerBattery is a battery meter with mode control, power setpoint and charge limit
func powerBattery(ctrl *gomock.Controller, charge float64) (api.Meter, *api.MockBatteryController, *api.MockBatteryPowerController) {
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
		BatteryPowerLimiter:    &mockBatteryPowerLimiter{charge: charge},
	}, batCon, batPow
}

// TestBatteryGridChargePower guards that a power controlled battery charges from the grid with exactly the
// headroom of the circuit and the slot's import budget instead of switching modes
func TestBatteryGridChargePower(t *testing.T) {
	for _, tc := range []struct {
		name        string
		headroom    float64 // circuit grant for the charge limit, 0 = no circuit
		importLimit float64 // grid import limit, 0 = none
		gridPower   float64
		expect      float64 // charge setpoint, 0 = released to normal
	}{
		{"circuit headroom", 4000, 0, 8000, 4000},
		{"full charge limit", 5000, 0, 8000, 5000},
		{"no circuit headroom releases to normal", 50, 0, 8000, 0},
		{"import limit without circuit", 0, 5000, 6000, 2000},
		{"circuit tighter than import limit", 1500, 5000, 6000, 1500},
		{"import limit exhausted releases to normal", 0, 5000, 9000, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)

			bat, batCon, batPow := powerBattery(ctrl, 5000)

			site := &Site{
				log:             util.NewLogger("foo"),
				batteryMeters:   []config.Device[api.Meter]{config.NewStaticDevice(config.Named{Name: "bat"}, bat)},
				batteryMode:     api.BatteryNormal,
				gridImportLimit: tc.importLimit,
				// the battery draws 3 kW, included in the grid power
				siteState: siteState{gridPower: tc.gridPower, battery: types.BatteryState{Power: -3000}},
			}

			if tc.headroom > 0 {
				circuit := api.NewMockCircuit(ctrl)
				circuit.EXPECT().HasMeter().Return(true).AnyTimes()
				circuit.EXPECT().ValidatePower(3000.0, 5000.0).Return(tc.headroom)
				circuit.EXPECT().ValidateCurrent(gomock.Any(), gomock.Any()).DoAndReturn(func(_, new float64) float64 {
					return new
				})
				site.circuit = circuit
			}

			if tc.expect > 0 {
				batPow.EXPECT().SetBatteryPower(-tc.expect)
			} else {
				batCon.EXPECT().SetBatteryMode(api.BatteryNormal)
			}

			site.updateBatteryMode(true, false, api.Rate{})

			if tc.expect > 0 {
				assert.Equal(t, api.BatteryCharge, site.GetBatteryMode())
			} else {
				assert.Equal(t, api.BatteryNormal, site.GetBatteryMode())
			}
			assert.InDelta(t, tc.expect, site.batteryChargeSetpoint, 1e-6)

			ctrl.Finish()
		})
	}
}

// TestBatteryGridChargePowerRefresh guards that the setpoint is written every cycle, the device holds it only briefly
func TestBatteryGridChargePowerRefresh(t *testing.T) {
	ctrl := gomock.NewController(t)

	bat, _, batPow := powerBattery(ctrl, 5000)
	batPow.EXPECT().SetBatteryPower(-5000.0).Times(3)

	site := &Site{
		log:           util.NewLogger("foo"),
		batteryMeters: []config.Device[api.Meter]{config.NewStaticDevice(config.Named{Name: "bat"}, bat)},
		batteryMode:   api.BatteryNormal,
		siteState:     siteState{battery: types.BatteryState{Power: -5000}},
	}

	for range 3 {
		site.updateBatteryMode(true, false, api.Rate{})
	}

	ctrl.Finish()
}

// TestBatteryChargeShares guards that the setpoint is split by the batteries' charge limits
func TestBatteryChargeShares(t *testing.T) {
	ctrl := gomock.NewController(t)

	big, _, _ := powerBattery(ctrl, 6000)
	small, _, _ := powerBattery(ctrl, 2000)

	site := &Site{
		batteryMeters: []config.Device[api.Meter]{
			config.NewStaticDevice(config.Named{Name: "big"}, big),
			config.NewStaticDevice(config.Named{Name: "small"}, small),
		},
	}

	assert.Equal(t, map[string]float64{"big": 3000, "small": 1000}, site.batteryChargeShares(4000))
}
