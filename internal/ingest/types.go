package ingest

import (
	"sort"
	"strings"
)

// Data type sets ingested by Ritm. Extra discrete types can be added through the
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

// MaxSleepSampleMS bounds how long one sleep sample is assumed to last (real ones stay under a
// day), so queries for an interval can also bound start_ms from below and use the
// records_type_start index instead of scanning all earlier sleep.
const MaxSleepSampleMS = 36 * 60 * 60 * 1000

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

// Workout categories: a small, stable grouping of HealthKit activity types for API clients.
const (
	CategoryWalk     = "walk"
	CategoryRun      = "run"
	CategoryHike     = "hike"
	CategoryCycle    = "cycle"
	CategorySwim     = "swim"
	CategoryRow      = "row"
	CategoryPaddle   = "paddle"
	CategorySail     = "sail"
	CategorySki      = "ski"
	CategoryStrength = "strength"
	CategoryCardio   = "cardio"
	CategoryOther    = "other"
)

// Categories lists every category, CategoryOther last.
var Categories = []string{CategoryWalk, CategoryRun, CategoryHike, CategoryCycle, CategorySwim, CategoryRow, CategoryPaddle,
	CategorySail, CategorySki, CategoryStrength, CategoryCardio, CategoryOther}

// workoutCategory maps activity types (without the HKWorkoutActivityType prefix); the rest are
// CategoryOther.
var workoutCategory = map[string]string{
	"Walking": CategoryWalk, "WheelchairWalkPace": CategoryWalk,
	"Running": CategoryRun, "WheelchairRunPace": CategoryRun, "TrackAndField": CategoryRun,
	"Hiking": CategoryHike, "Climbing": CategoryHike,
	"Cycling": CategoryCycle, "HandCycling": CategoryCycle,
	"Swimming": CategorySwim, "WaterFitness": CategorySwim,
	"Rowing":       CategoryRow,
	"PaddleSports": CategoryPaddle, "SurfingSports": CategoryPaddle,
	"Sailing":        CategorySail,
	"DownhillSkiing": CategorySki, "CrossCountrySkiing": CategorySki, "Snowboarding": CategorySki, "SnowSports": CategorySki,
	"SkatingSports":               CategorySki,
	"TraditionalStrengthTraining": CategoryStrength, "FunctionalStrengthTraining": CategoryStrength,
	"CoreTraining":                  CategoryStrength,
	"HighIntensityIntervalTraining": CategoryCardio, "MixedCardio": CategoryCardio, "CrossTraining": CategoryCardio,
	"Elliptical": CategoryCardio, "StairClimbing": CategoryCardio, "Stairs": CategoryCardio, "StepTraining": CategoryCardio,
	"JumpRope": CategoryCardio, "Kickboxing": CategoryCardio, "Boxing": CategoryCardio, "CardioDance": CategoryCardio,
	"SocialDance": CategoryCardio, "Dance": CategoryCardio,
}

// distanceBased are the categories whose workouts cover a distance (and usually have a route).
var distanceBased = map[string]bool{CategoryWalk: true, CategoryRun: true, CategoryHike: true, CategoryCycle: true,
	CategorySwim: true, CategoryRow: true, CategoryPaddle: true, CategorySail: true, CategorySki: true}

// WorkoutCategory returns the category of a HealthKit activity type and whether its workouts are
// distance based.
func WorkoutCategory(activityType string) (string, bool) {
	c, ok := workoutCategory[strings.TrimPrefix(activityType, "HKWorkoutActivityType")]
	if !ok {
		c = CategoryOther
	}
	return c, distanceBased[c]
}

// CategoryTypes returns the activity types of category c; for CategoryOther, the types that are
// in no other category (to be excluded).
func CategoryTypes(c string) []string {
	var out []string
	for t, cat := range workoutCategory {
		if cat == c || (c == CategoryOther) {
			out = append(out, "HKWorkoutActivityType"+t)
		}
	}
	sort.Strings(out)
	return out
}
