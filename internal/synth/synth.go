// Package synth generates deterministic synthetic Apple Health exports for tests and benchmarks.
package synth

import (
	"archive/zip"
	"bufio"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"os"
	"time"
)

type Options struct {
	Start      time.Time // first day (local midnight in Loc)
	Days       int
	Loc        *time.Location
	Seed       uint64
	ExportDate time.Time // defaults to the end of the last day
	IPhone     bool      // add overlapping iPhone steps/distance
	Sleep      bool      // add nightly sleep stages
	// HRIntervalSec is the spacing of passive heart-rate samples (default 300).
	HRIntervalSec int
	// WorkoutHRIntervalSec is the spacing of heart-rate samples during workouts (default 5).
	WorkoutHRIntervalSec int
	// WorkoutDays lists weekdays with a 60-minute strength session at 18:00 (default Mon/Wed/Fri).
	WorkoutDays []time.Weekday
	// StepScale multiplies step counts, to simulate "walking less" periods.
	StepScale func(day int) float64
	// ControlChars injects invalid XML control characters into a metadata value.
	ControlChars bool
	// Folder is the zip folder holding export.xml (default "apple_health_export").
	Folder string
	// Walks adds a 45-minute outdoor walk with a GPS route every Saturday at 10:00; WriteZip
	// stores the route files under workout-routes/.
	Walks bool
}

const (
	watchSource = "Apple Watch Oleksandr"
	phoneSource = "iPhone Oleksandr"
	watchDevice = "&lt;&lt;HKDevice: 0x283d5c0f0&gt;, name:Apple Watch, manufacturer:Apple Inc., model:Watch, hardware:Watch6,18, software:10.0&gt;"
	phoneDevice = "&lt;&lt;HKDevice: 0x283d5c1a0&gt;, name:iPhone, manufacturer:Apple Inc., model:iPhone, hardware:iPhone15,2, software:17.0&gt;"
)

const header = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE HealthData [
<!-- HealthKit Export Version: 13 -->
<!ELEMENT HealthData (ExportDate,Me,(Record|Correlation|Workout|ActivitySummary|ClinicalRecord|Audiogram|VisionPrescription)*)>
<!ATTLIST HealthData
  locale CDATA #REQUIRED
>
<!ELEMENT ExportDate EMPTY>
<!ATTLIST ExportDate
  value CDATA #REQUIRED
>
<!ELEMENT Record ((MetadataEntry|HeartRateVariabilityMetadataList)*)>
<!ATTLIST Record
  type          CDATA #REQUIRED
  unit          CDATA #IMPLIED
  value         CDATA #IMPLIED
  sourceName    CDATA #REQUIRED
  startDate     CDATA #REQUIRED
  endDate       CDATA #REQUIRED
  startDate     CDATA #REQUIRED
>
]>
<HealthData locale="uk_UA">
`

func (o *Options) defaults() {
	if o.Loc == nil {
		o.Loc = time.FixedZone("EET", 2*3600)
	}
	if o.Days == 0 {
		o.Days = 14
	}
	if o.HRIntervalSec == 0 {
		o.HRIntervalSec = 300
	}
	if o.WorkoutHRIntervalSec == 0 {
		o.WorkoutHRIntervalSec = 5
	}
	if o.WorkoutDays == nil {
		o.WorkoutDays = []time.Weekday{time.Monday, time.Wednesday, time.Friday}
	}
	if o.Start.IsZero() {
		o.Start = time.Date(2026, 1, 5, 0, 0, 0, 0, o.Loc)
	}
	if o.ExportDate.IsZero() {
		o.ExportDate = o.Start.AddDate(0, 0, o.Days).Add(-time.Minute)
	}
	if o.Folder == "" {
		o.Folder = "apple_health_export"
	}
}

type gen struct {
	w   *bufio.Writer
	o   Options
	rnd *rand.Rand
}

func fmtDate(t time.Time) string { return t.Format("2006-01-02 15:04:05 -0700") }

// WriteXML writes export.xml content to w.
func WriteXML(w io.Writer, o Options) error {
	o.defaults()
	g := &gen{w: bufio.NewWriterSize(w, 1<<16), o: o, rnd: rand.New(rand.NewPCG(o.Seed, 0x5eed))}
	g.w.WriteString(header)
	fmt.Fprintf(g.w, " <ExportDate value=\"%s\"/>\n", fmtDate(o.ExportDate.In(o.Loc)))
	g.w.WriteString(` <Me HKCharacteristicTypeIdentifierDateOfBirth="1986-04-12" HKCharacteristicTypeIdentifierBiologicalSex="HKBiologicalSexMale" HKCharacteristicTypeIdentifierBloodType="HKBloodTypeNotSet" HKCharacteristicTypeIdentifierFitzpatrickSkinType="HKFitzpatrickSkinTypeNotSet" HKCharacteristicTypeIdentifierCardioFitnessMedicationsUse="None"/>` + "\n")

	type wo struct{ start, end time.Time }
	var workouts []wo
	for d := 0; d < o.Days; d++ {
		day := o.Start.AddDate(0, 0, d)
		if g.isWorkoutDay(day) {
			workouts = append(workouts, wo{day.Add(18 * time.Hour), day.Add(19 * time.Hour)})
		}
	}
	// Records are grouped by type like real exports.
	for d := 0; d < o.Days; d++ {
		g.daySteps(d)
	}
	for d := 0; d < o.Days; d++ {
		g.dayHR(d)
	}
	for d := 0; d < o.Days; d++ {
		g.dayOther(d)
	}
	for _, w := range workouts {
		g.workout(w.start, w.end)
	}
	for _, start := range o.walks() {
		g.walk(start)
	}
	for d := 0; d < o.Days; d++ {
		g.summary(d)
	}
	g.w.WriteString("</HealthData>\n")
	return g.w.Flush()
}

// WriteZip writes a zip containing <Folder>/export.xml to path.
func WriteZip(path string, o Options) error {
	o.defaults()
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(f)
	ew, err := zw.CreateHeader(&zip.FileHeader{Name: o.Folder + "/export.xml", Method: zip.Deflate})
	if err != nil {
		f.Close()
		return err
	}
	if err := WriteXML(ew, o); err != nil {
		f.Close()
		return err
	}
	for _, start := range o.walks() {
		rw, err := zw.Create(o.Folder + RoutePath(start))
		if err != nil {
			f.Close()
			return err
		}
		writeGPX(rw, start)
	}
	cw, _ := zw.Create(o.Folder + "/export_cda.xml")
	io.WriteString(cw, "<ClinicalDocument/>")
	if err := zw.Close(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// seedDay reseeds per day and section so overlapping exports contain identical samples.
func (g *gen) seedDay(d int, section uint64) {
	g.rnd = rand.New(rand.NewPCG(g.o.Seed, uint64(d)*8+section))
}

func (g *gen) isWorkoutDay(day time.Time) bool {
	for _, wd := range g.o.WorkoutDays {
		if day.Weekday() == wd {
			return true
		}
	}
	return false
}

func (g *gen) rec(typ, source, device, unit string, start, end time.Time, value string, meta ...string) {
	w := g.w
	w.WriteString(` <Record type="`)
	w.WriteString(typ)
	w.WriteString(`" sourceName="`)
	w.WriteString(source)
	w.WriteString(`" sourceVersion="10.0" device="`)
	w.WriteString(device)
	w.WriteString(`"`)
	if unit != "" {
		w.WriteString(` unit="`)
		w.WriteString(unit)
		w.WriteString(`"`)
	}
	ed := fmtDate(end)
	w.WriteString(` creationDate="` + ed + `" startDate="` + fmtDate(start) + `" endDate="` + ed + `" value="` + value + `"`)
	if len(meta) == 0 {
		w.WriteString("/>\n")
		return
	}
	w.WriteString(">\n")
	for i := 0; i+1 < len(meta); i += 2 {
		w.WriteString(`  <MetadataEntry key="` + meta[i] + `" value="` + meta[i+1] + `"/>` + "\n")
	}
	w.WriteString(" </Record>\n")
}

func (g *gen) scale(d int) float64 {
	if g.o.StepScale != nil {
		return g.o.StepScale(d)
	}
	return 1
}

// daySteps emits steps, distance and energy samples: 10-minute Watch samples while awake.
func (g *gen) daySteps(d int) {
	g.seedDay(d, 1)
	day := g.o.Start.AddDate(0, 0, d)
	for m := 8 * 60; m < 23*60; m += 10 {
		start := day.Add(time.Duration(m) * time.Minute)
		end := start.Add(10 * time.Minute)
		steps := math.Round(float64(40+g.rnd.IntN(120)) * g.scale(d))
		g.rec("HKQuantityTypeIdentifierStepCount", watchSource, watchDevice, "count", start, end, ftoa(steps, 0))
		g.rec("HKQuantityTypeIdentifierDistanceWalkingRunning", watchSource, watchDevice, "km", start, end, ftoa(steps*0.00075, 5))
		if g.o.IPhone {
			// The phone counts a bit less and its samples are offset by 3 minutes.
			ps, pe := start.Add(3*time.Minute), end.Add(3*time.Minute)
			g.rec("HKQuantityTypeIdentifierStepCount", phoneSource, phoneDevice, "count", ps, pe, ftoa(math.Round(steps*0.8), 0))
			g.rec("HKQuantityTypeIdentifierDistanceWalkingRunning", phoneSource, phoneDevice, "km", ps, pe, ftoa(steps*0.8*0.00075, 5))
		}
	}
	for h := 0; h < 24; h++ {
		start := day.Add(time.Duration(h) * time.Hour)
		end := start.Add(time.Hour)
		active := 10 + float64(g.rnd.IntN(25))
		if h >= 8 && h < 23 {
			active += 10
		}
		if h == 18 && g.isWorkoutDay(day) {
			active += 380
			g.rec("HKQuantityTypeIdentifierAppleExerciseTime", watchSource, watchDevice, "min", start, end, "60")
		} else if h >= 8 && h < 23 && g.rnd.IntN(4) == 0 {
			g.rec("HKQuantityTypeIdentifierAppleExerciseTime", watchSource, watchDevice, "min", start, start.Add(5*time.Minute), "5")
		}
		g.rec("HKQuantityTypeIdentifierActiveEnergyBurned", watchSource, watchDevice, "Cal", start, end, ftoa(active, 3))
		g.rec("HKQuantityTypeIdentifierBasalEnergyBurned", watchSource, watchDevice, "Cal", start, end, "72.5")
		if h >= 8 && h < 23 {
			g.rec("HKQuantityTypeIdentifierAppleStandTime", watchSource, watchDevice, "min", start, start.Add(4*time.Minute), "4")
			if g.rnd.IntN(5) == 0 {
				g.rec("HKQuantityTypeIdentifierFlightsClimbed", watchSource, watchDevice, "count", start, start.Add(time.Minute), "1")
			}
		}
	}
}

// dayHR emits passive heart rate plus dense samples during workouts.
func (g *gen) dayHR(d int) {
	g.seedDay(d, 2)
	day := g.o.Start.AddDate(0, 0, d)
	wStart, wEnd := day.Add(18*time.Hour), day.Add(19*time.Hour)
	gym := g.isWorkoutDay(day)
	for s := 0; s < 86400; s += g.o.HRIntervalSec {
		t := day.Add(time.Duration(s) * time.Second)
		if gym && !t.Before(wStart) && t.Before(wEnd.Add(2*time.Minute)) {
			continue
		}
		base := 62.0
		if s < 7*3600 {
			base = 54
		}
		g.rec("HKQuantityTypeIdentifierHeartRate", watchSource, watchDevice, "count/min", t, t, ftoa(base+float64(g.rnd.IntN(20)), 0),
			"HKMetadataKeyHeartRateMotionContext", "0")
	}
	if gym {
		for s := 0; s < 3600+120; s += g.o.WorkoutHRIntervalSec {
			t := wStart.Add(time.Duration(s) * time.Second)
			hr := 105 + 45*math.Sin(float64(s)/3600*math.Pi) + float64(g.rnd.IntN(10))
			if s >= 3600 {
				hr = 120 - float64(s-3600)/4
			}
			g.rec("HKQuantityTypeIdentifierHeartRate", watchSource, watchDevice, "count/min", t, t, ftoa(hr, 0),
				"HKMetadataKeyHeartRateMotionContext", "2")
		}
	}
}

func (g *gen) dayOther(d int) {
	g.seedDay(d, 3)
	day := g.o.Start.AddDate(0, 0, d)
	at := func(h, m int) time.Time { return day.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute) }
	g.rec("HKQuantityTypeIdentifierRestingHeartRate", watchSource, watchDevice, "count/min", at(0, 0), at(23, 59), ftoa(float64(56+g.rnd.IntN(4)), 0))
	g.rec("HKQuantityTypeIdentifierWalkingHeartRateAverage", watchSource, watchDevice, "count/min", at(0, 0), at(23, 59), ftoa(float64(92+g.rnd.IntN(6)), 0))
	for _, h := range []int{3, 9, 15} {
		meta := []string{}
		if g.o.ControlChars && d == 0 && h == 3 {
			meta = []string{"HKMetadataKeyNote", "bad\x01char"}
		}
		g.rec("HKQuantityTypeIdentifierHeartRateVariabilitySDNN", watchSource, watchDevice, "ms", at(h, 0), at(h, 1), ftoa(40+g.rnd.Float64()*20, 4), meta...)
	}
	if d%7 == 0 {
		g.rec("HKQuantityTypeIdentifierVO2Max", watchSource, watchDevice, "mL/min·kg", at(12, 0), at(12, 0), ftoa(38+float64(d)/100, 2),
			"HKMetadataKeyVO2MaxTestType", "2")
	}
	g.rec("HKQuantityTypeIdentifierOxygenSaturation", watchSource, watchDevice, "%", at(4, 0), at(4, 0), ftoa(0.95+g.rnd.Float64()*0.04, 2))
	for h := 8; h < 23; h++ {
		v := "HKCategoryValueAppleStandHourStood"
		if g.rnd.IntN(4) == 0 {
			v = "HKCategoryValueAppleStandHourIdle"
		}
		g.rec("HKCategoryTypeIdentifierAppleStandHour", watchSource, watchDevice, "", at(h, 0), at(h+1, 0), v)
	}
	if g.o.Sleep && d > 0 {
		// Night from the previous day 23:30 to 07:00, assigned to this wake-up day.
		bed := at(0, 0).Add(-30 * time.Minute)
		g.rec("HKCategoryTypeIdentifierSleepAnalysis", watchSource, watchDevice, "", bed, at(7, 0), "HKCategoryValueSleepAnalysisInBed")
		t := bed.Add(10 * time.Minute)
		stages := []struct {
			v   string
			min int
		}{{"AsleepCore", 90}, {"AsleepDeep", 50}, {"AsleepREM", 40}, {"Awake", 5}, {"AsleepCore", 120}, {"AsleepREM", 60}, {"AsleepCore", 55}}
		for _, s := range stages {
			end := t.Add(time.Duration(s.min) * time.Minute)
			g.rec("HKCategoryTypeIdentifierSleepAnalysis", watchSource, watchDevice, "", t, end, "HKCategoryValueSleepAnalysis"+s.v)
			t = end
		}
		g.rec("HKQuantityTypeIdentifierRespiratoryRate", watchSource, watchDevice, "count/min", at(3, 0), at(3, 0), ftoa(14+g.rnd.Float64()*2, 1))
		g.rec("HKQuantityTypeIdentifierAppleSleepingWristTemperature", watchSource, watchDevice, "degC", bed, at(7, 0), ftoa(35.0+g.rnd.Float64()*0.6, 2))
	}
	if d == 3 {
		g.rec("HKCategoryTypeIdentifierHighHeartRateEvent", watchSource, watchDevice, "", at(14, 0), at(14, 10), "HKCategoryValueNotApplicable",
			"HKHeartRateEventThreshold", "120 count/min")
	}
}

func (g *gen) workout(start, end time.Time) {
	w := g.w
	fmt.Fprintf(w, ` <Workout workoutActivityType="HKWorkoutActivityTypeFunctionalStrengthTraining" duration="60" durationUnit="min" sourceName="%s" sourceVersion="10.0" device="%s" creationDate="%s" startDate="%s" endDate="%s">`+"\n",
		watchSource, watchDevice, fmtDate(end), fmtDate(start), fmtDate(end))
	w.WriteString(`  <MetadataEntry key="HKIndoorWorkout" value="1"/>` + "\n")
	w.WriteString(`  <MetadataEntry key="HKTimeZone" value="Europe/Kiev"/>` + "\n")
	pause := start.Add(25 * time.Minute)
	fmt.Fprintf(w, `  <WorkoutEvent type="HKWorkoutEventTypePause" date="%s"/>`+"\n", fmtDate(pause))
	fmt.Fprintf(w, `  <WorkoutEvent type="HKWorkoutEventTypeResume" date="%s"/>`+"\n", fmtDate(pause.Add(2*time.Minute)))
	fmt.Fprintf(w, `  <WorkoutEvent type="HKWorkoutEventTypeSegment" date="%s" duration="60" durationUnit="min"/>`+"\n", fmtDate(start))
	fmt.Fprintf(w, `  <WorkoutStatistics type="HKQuantityTypeIdentifierActiveEnergyBurned" startDate="%s" endDate="%s" sum="402.5" unit="Cal"/>`+"\n", fmtDate(start), fmtDate(end))
	fmt.Fprintf(w, `  <WorkoutStatistics type="HKQuantityTypeIdentifierBasalEnergyBurned" startDate="%s" endDate="%s" sum="72.1" unit="Cal"/>`+"\n", fmtDate(start), fmtDate(end))
	fmt.Fprintf(w, `  <WorkoutStatistics type="HKQuantityTypeIdentifierHeartRate" startDate="%s" endDate="%s" average="128" minimum="92" maximum="161" unit="count/min"/>`+"\n", fmtDate(start), fmtDate(end))
	fmt.Fprintf(w, `  <WorkoutActivity uuid="A1" startDate="%s" endDate="%s" duration="60" durationUnit="min">`+"\n", fmtDate(start), fmtDate(end))
	fmt.Fprintf(w, `   <WorkoutStatistics type="HKQuantityTypeIdentifierActiveEnergyBurned" startDate="%s" endDate="%s" sum="402.5" unit="Cal"/>`+"\n", fmtDate(start), fmtDate(end))
	w.WriteString("  </WorkoutActivity>\n")
	w.WriteString(" </Workout>\n")
}

// WalkMinutes is the duration of a synthetic walk; WalkPoints its number of GPS points.
const (
	WalkMinutes = 45
	WalkPoints  = WalkMinutes * 60 / 5
)

func (o Options) walks() []time.Time {
	if !o.Walks {
		return nil
	}
	var out []time.Time
	for d := 0; d < o.Days; d++ {
		if day := o.Start.AddDate(0, 0, d); day.Weekday() == time.Saturday {
			out = append(out, day.Add(10*time.Hour))
		}
	}
	return out
}

// RoutePath is the zip path (below the export folder) of the route of a walk starting at start.
func RoutePath(start time.Time) string {
	return "/workout-routes/route_" + start.Format("2006-01-02_3.04pm") + ".gpx"
}

func (g *gen) walk(start time.Time) {
	end := start.Add(WalkMinutes * time.Minute)
	fmt.Fprintf(g.w, ` <Workout workoutActivityType="HKWorkoutActivityTypeWalking" duration="%d" durationUnit="min" totalDistance="3.6" totalDistanceUnit="km" sourceName="%s" sourceVersion="10.0" device="%s" creationDate="%s" startDate="%s" endDate="%s">`+"\n",
		WalkMinutes, watchSource, watchDevice, fmtDate(end), fmtDate(start), fmtDate(end))
	g.w.WriteString(`  <MetadataEntry key="HKIndoorWorkout" value="0"/>` + "\n")
	fmt.Fprintf(g.w, "  <WorkoutRoute sourceName=\"%s\" startDate=\"%s\" endDate=\"%s\">\n   <FileReference path=\"%s\"/>\n  </WorkoutRoute>\n",
		watchSource, fmtDate(start), fmtDate(end), RoutePath(start))
	g.w.WriteString(" </Workout>\n")
}

// writeGPX writes a walk heading north-east at about 1.3 m/s with a point every 5 s, climbing
// 10 m over the first half and descending again.
func writeGPX(w io.Writer, start time.Time) {
	bw := bufio.NewWriter(w)
	bw.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<gpx version="1.1" creator="Apple Health Export" xmlns="http://www.topografix.com/GPX/1/1"><trk><name>Route</name><trkseg>
`)
	const lat0, lon0 = 50.45, 30.52
	const step = 1.3 * 5 / 111_320 / math.Sqrt2 // degrees of latitude per point
	for i := range WalkPoints {
		ele := 150 + 10*math.Sin(math.Pi*float64(i)/float64(WalkPoints-1))
		fmt.Fprintf(bw, "<trkpt lat=\"%.7f\" lon=\"%.7f\"><ele>%.2f</ele><time>%s</time></trkpt>\n",
			lat0+float64(i)*step, lon0+float64(i)*step/math.Cos(lat0*math.Pi/180), ele,
			start.Add(time.Duration(i)*5*time.Second).UTC().Format(time.RFC3339))
	}
	bw.WriteString("</trkseg></trk></gpx>\n")
	bw.Flush()
}

func (g *gen) summary(d int) {
	g.seedDay(d, 4)
	day := g.o.Start.AddDate(0, 0, d)
	ex := 15
	if g.isWorkoutDay(day) {
		ex = 75
	}
	fmt.Fprintf(g.w, ` <ActivitySummary dateComponents="%s" activeEnergyBurned="%d" activeEnergyBurnedGoal="500" activeEnergyBurnedUnit="Cal" appleMoveTime="0" appleMoveTimeGoal="0" appleExerciseTime="%d" appleExerciseTimeGoal="30" appleStandHours="%d" appleStandHoursGoal="12"/>`+"\n",
		day.Format("2006-01-02"), 450+g.rnd.IntN(200), ex, 9+g.rnd.IntN(5))
}

func ftoa(v float64, prec int) string { return fmt.Sprintf("%.*f", prec, v) }
