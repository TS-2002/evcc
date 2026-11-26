package meter

import (
	"context"
	"testing"

	"github.com/evcc-io/evcc/api"
	"github.com/evcc-io/evcc/plugin"
	"github.com/evcc-io/evcc/util"
	"github.com/stretchr/testify/require"
)

// TestBatteryLimitSocWithBatteryMode covers limitsoc and batterymode configured together:
// the reserve soc is written first, then the mode
func TestBatteryLimitSocWithBatteryMode(t *testing.T) {
	ctx := context.TODO()

	js := func(script string) map[string]any {
		return map[string]any{"source": "js", "vm": "compose", "script": script}
	}

	m, err := NewConfigurableFromConfig(ctx, map[string]any{
		"power":        map[string]any{"source": "const", "value": 0},
		"soc":          map[string]any{"source": "const", "value": 42},
		"minsoc":       10,
		"maxsoc":       80,
		"limitsoc":     js(`log = (typeof log === "undefined" ? "" : log) + "L" + limitSoc + ";"`),
		"batterymode":  js(`log = (typeof log === "undefined" ? "" : log) + "M" + batteryMode + ";"`),
		"batterymodes": []string{"normal", "holddischarge", "charge", "holdcharge"},
	})
	require.NoError(t, err)

	ctrl, ok := api.Cap[api.BatteryController](m)
	require.True(t, ok)
	require.Equal(t, []api.BatteryMode{api.BatteryNormal, api.BatteryHoldDischarge, api.BatteryCharge, api.BatteryHoldCharge}, ctrl.BatteryModes())

	for _, mode := range ctrl.BatteryModes() {
		require.NoError(t, ctrl.SetBatteryMode(mode))
	}

	var cc plugin.Config
	require.NoError(t, util.DecodeOther(js("log"), &cc))
	logG, err := cc.StringGetter(ctx)
	require.NoError(t, err)

	log, err := logG()
	require.NoError(t, err)

	// holdcharge is not expressible via limit soc, only the mode is written
	require.Equal(t, "L10;M1;L42;M2;L80;M3;M4;", log)
}

// TestBatteryPower covers the batterypower setter: it receives the signed setpoint and requires batterymode
func TestBatteryPower(t *testing.T) {
	ctx := context.TODO()

	js := func(script string) map[string]any {
		return map[string]any{"source": "js", "vm": "power", "script": script}
	}

	base := func() map[string]any {
		return map[string]any{
			"power":             map[string]any{"source": "const", "value": 0},
			"soc":               map[string]any{"source": "const", "value": 42},
			"batterymode":       js(`batteryMode`),
			"batterymodes":      []string{"normal", "charge"},
			"maxchargepower":    5000,
			"maxdischargepower": 3000,
		}
	}

	t.Run("setpoint", func(t *testing.T) {
		cfg := base()
		cfg["batterypower"] = js(`log = (typeof log === "undefined" ? "" : log) + batteryPower + ";"`)

		m, err := NewConfigurableFromConfig(ctx, cfg)
		require.NoError(t, err)

		ctrl, ok := api.Cap[api.BatteryPowerController](m)
		require.True(t, ok)

		for _, p := range []float64{-2000, 0, 1500} {
			require.NoError(t, ctrl.SetBatteryPower(p))
		}

		var cc plugin.Config
		require.NoError(t, util.DecodeOther(js("log"), &cc))
		logG, err := cc.StringGetter(ctx)
		require.NoError(t, err)

		log, err := logG()
		require.NoError(t, err)
		require.Equal(t, "-2000;0;1500;", log)
	})

	t.Run("requires batterymode", func(t *testing.T) {
		cfg := base()
		delete(cfg, "batterymode")
		delete(cfg, "batterymodes")
		cfg["batterypower"] = js(`batteryPower`)

		_, err := NewConfigurableFromConfig(ctx, cfg)
		require.ErrorContains(t, err, "requires batterymode")
	})

	t.Run("requires power limits", func(t *testing.T) {
		cfg := base()
		delete(cfg, "maxchargepower")
		delete(cfg, "maxdischargepower")
		cfg["batterypower"] = js(`batteryPower`)

		_, err := NewConfigurableFromConfig(ctx, cfg)
		require.ErrorContains(t, err, "requires maxchargepower")
	})

	t.Run("optional", func(t *testing.T) {
		m, err := NewConfigurableFromConfig(ctx, base())
		require.NoError(t, err)

		_, ok := api.Cap[api.BatteryPowerController](m)
		require.False(t, ok)
	})
}
