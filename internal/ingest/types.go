package ingest

import "strings"

// Data type sets ingested by Ritm (§6.3). Extra discrete types can be added through the
// extra_types setting (Settings → stored in the "app" row); they need a full re-import to backfill.

const (
	TypeHeartRate        = "HKQuantityTypeIdentifierHeartRate"
	TypeRestingHR        = "HKQuantityTypeIdentifierRestingHeartRate"
	TypeWalkingHR        = "HKQuantityTypeIdentifierWalkingHeartRateAverage"
	TypeHRV              = "HKQuantityTypeIdentifierHeartRateVariabilitySDNN"
	TypeHRRecovery       = "HKQuantityTypeIdentifierHeartRateRecoveryOneMinute"
	TypeVO2Max           = "HKQuantityTypeIdentifierVO2Max"
	TypeSpO2             = "HKQuantityTypeIdentifierOxygenSaturation"
	TypeRespiratoryRate  = "HKQuantityTypeIdentifierRespiratoryRate"
	TypeWristTemperature = "HKQuantityTypeIdentifierAppleSleepingWristTemperature"
	TypeBodyMass         = "HKQuantityTypeIdentifierBodyMass"
	TypeSleep            = "HKCategoryTypeIdentifierSleepAnalysis"
	TypeHighHREvent      = "HKCategoryTypeIdentifierHighHeartRateEvent"
	TypeLowHREvent       = "HKCategoryTypeIdentifierLowHeartRateEvent"
	TypeIrregularRhythm  = "HKCategoryTypeIdentifierIrregularHeartRhythmEvent"
	TypeStandHour        = "HKCategoryTypeIdentifierAppleStandHour"

	TypeSteps        = "HKQuantityTypeIdentifierStepCount"
	TypeDistance     = "HKQuantityTypeIdentifierDistanceWalkingRunning"
	TypeActiveEnergy = "HKQuantityTypeIdentifierActiveEnergyBurned"
	TypeBasalEnergy  = "HKQuantityTypeIdentifierBasalEnergyBurned"
	TypeFlights      = "HKQuantityTypeIdentifierFlightsClimbed"
	TypeExerciseTime = "HKQuantityTypeIdentifierAppleExerciseTime"
	TypeStandTime    = "HKQuantityTypeIdentifierAppleStandTime"

	WorkoutFunctionalStrength = "HKWorkoutActivityTypeFunctionalStrengthTraining"
)

// DiscreteTypes are stored raw in records.
var DiscreteTypes = []string{
	TypeHeartRate, TypeRestingHR, TypeWalkingHR, TypeHRV, TypeHRRecovery, TypeVO2Max,
	TypeSpO2, TypeRespiratoryRate, TypeWristTemperature,
	"HKQuantityTypeIdentifierWalkingSpeed", "HKQuantityTypeIdentifierWalkingStepLength",
	"HKQuantityTypeIdentifierWalkingAsymmetryPercentage", "HKQuantityTypeIdentifierWalkingDoubleSupportPercentage",
	TypeBodyMass, TypeSleep, TypeHighHREvent, TypeLowHREvent, TypeIrregularRhythm, TypeStandHour,
}

// CumulativeTypes are aggregated into hourly buckets; raw samples are not kept.
var CumulativeTypes = []string{
	TypeSteps, TypeDistance, TypeActiveEnergy, TypeBasalEnergy, TypeFlights, TypeExerciseTime, TypeStandTime,
}

// metadataSkipTypes are too numerous to keep per-sample metadata for.
var metadataSkipTypes = map[string]bool{TypeHeartRate: true}

type typeSet struct {
	discrete   map[string]bool
	cumulative map[string]bool
}

func newTypeSet(extra []string) typeSet {
	ts := typeSet{discrete: map[string]bool{}, cumulative: map[string]bool{}}
	for _, t := range DiscreteTypes {
		ts.discrete[t] = true
	}
	for _, t := range extra {
		ts.discrete[t] = true
	}
	for _, t := range CumulativeTypes {
		ts.cumulative[t] = true
		delete(ts.discrete, t)
	}
	return ts
}

func (ts typeSet) wanted(t string) bool { return ts.discrete[t] || ts.cumulative[t] }

// WorkoutName turns HKWorkoutActivityTypeFunctionalStrengthTraining into "Functional Strength
// Training". Words start where a lowercase letter meets an uppercase one, so "VO2Max" stays whole.
func WorkoutName(t string) string {
	t = strings.TrimPrefix(t, "HKWorkoutActivityType")
	var b strings.Builder
	var prev rune
	for _, r := range t {
		if prev >= 'a' && prev <= 'z' && r >= 'A' && r <= 'Z' {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
		prev = r
	}
	return b.String()
}
