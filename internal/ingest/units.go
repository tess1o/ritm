package ingest

import "strings"

// fractionPercentTypes are stored by HealthKit as 0–1 fractions with unit "%".
var fractionPercentTypes = map[string]bool{
	"HKQuantityTypeIdentifierOxygenSaturation":               true,
	"HKQuantityTypeIdentifierWalkingAsymmetryPercentage":     true,
	"HKQuantityTypeIdentifierWalkingDoubleSupportPercentage": true,
	"HKQuantityTypeIdentifierBodyFatPercentage":              true,
}

// normalize converts a value to Ritm's canonical units (PAR-6): distance → m, energy → kcal,
// time → s, mass → kg, temperature → degC, speed → m/s, HR → bpm, percentages → 0–100.
func normalize(typ, unit string, v float64) (string, float64) {
	switch unit {
	// distance
	case "m":
		return "m", v
	case "km":
		return "m", v * 1000
	case "cm":
		return "m", v / 100
	case "mm":
		return "m", v / 1000
	case "mi":
		return "m", v * 1609.344
	case "ft":
		return "m", v * 0.3048
	case "in":
		return "m", v * 0.0254
	case "yd":
		return "m", v * 0.9144
	// energy
	case "kcal", "Cal":
		return "kcal", v
	case "cal":
		return "kcal", v / 1000
	case "kJ":
		return "kcal", v / 4.184
	case "J":
		return "kcal", v / 4184
	// time (HRV is "ms" and intentionally kept in ms)
	case "s":
		return "s", v
	case "min":
		return "s", v * 60
	case "hr", "h":
		return "s", v * 3600
	case "d":
		return "s", v * 86400
	// mass
	case "kg":
		return "kg", v
	case "g":
		return "kg", v / 1000
	case "lb":
		return "kg", v * 0.45359237
	case "st":
		return "kg", v * 6.35029318
	// temperature
	case "degC":
		return "degC", v
	case "degF":
		return "degC", (v - 32) * 5 / 9
	// speed
	case "m/s":
		return "m/s", v
	case "km/hr":
		return "m/s", v / 3.6
	case "mi/hr":
		return "m/s", v * 0.44704
	// heart rate and other per-minute rates
	case "count/min":
		return "bpm", v
	case "count/s":
		return "bpm", v * 60
	case "%":
		if fractionPercentTypes[typ] {
			return "%", v * 100
		}
		return "%", v
	}
	if strings.HasPrefix(unit, "mL/") && strings.Contains(unit, "kg") {
		return "mL/(kg·min)", v
	}
	return unit, v
}
