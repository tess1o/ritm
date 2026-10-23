package ingest

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

type Record struct {
	Type, Source, Unit string
	Value              float64
	HasValue           bool
	Category           string
	StartMS, EndMS     int64
	Off                int
	Meta               [][2]string
	SourceKind         string
}

type WorkoutStat struct {
	Type           string   `json:"-"`
	Unit           string   `json:"unit,omitempty"`
	Sum            *float64 `json:"sum,omitempty"`
	Avg            *float64 `json:"avg,omitempty"`
	Min            *float64 `json:"min,omitempty"`
	Max            *float64 `json:"max,omitempty"`
	StartMS, EndMS int64    `json:"-"`
}

type WorkoutEvent struct {
	Type    string
	StartMS int64
	EndMS   int64 // 0 when the event has no duration
}

type WorkoutActivity struct {
	Type           string
	StartMS, EndMS int64
	Stats          []WorkoutStat
}

type Workout struct {
	ActivityType, Source string
	StartMS, EndMS       int64
	Off                  int
	DurationS            *float64
	DistanceM            *float64
	EnergyKcal           *float64
	Indoor               *bool
	Meta                 map[string]string
	Stats                []WorkoutStat
	Events               []WorkoutEvent
	Activities           []WorkoutActivity
	RoutePaths           []string
	SourceKind           string
}

type Summary struct {
	Day                          string
	ActiveKcal, ActiveKcalGoal   *float64
	MoveMin, MoveMinGoal         *float64
	ExerciseMin, ExerciseMinGoal *float64
	StandHours, StandHoursGoal   *float64
}

type Me struct {
	DOB string // YYYY-MM-DD
	Sex string // HKBiologicalSex…
}

type SourceInfo struct {
	Name            string
	Kind            string // watch | phone | other
	FirstMS, LastMS int64
}

// Handler receives parsed elements. Records arrive only for wanted types.
type Handler interface {
	ExportDate(ms int64)
	Me(Me)
	Record(*Record)
	Workout(*Workout)
	Summary(*Summary)
}

// ParseError is an XML syntax error that fails the import.
type ParseError struct {
	Offset int64
	Err    error
}

func (e *ParseError) Error() string { return fmt.Sprintf("XML error at byte %d: %v", e.Offset, e.Err) }
func (e *ParseError) Unwrap() error { return e.Err }

// Parser walks export.xml with RawToken, never building a DOM.
type Parser struct {
	dec   *xml.Decoder
	guard *tokenGuard
	loc   *time.Location
	types typeSet
	h     Handler
	depth int

	Skipped  map[string]int // malformed elements per type
	Problems []string       // details of the first malformed elements
	Ignored  int            // records of types Ritm does not ingest
	Sources  map[string]*SourceInfo
}

func NewParser(r io.Reader, loc *time.Location, types typeSet, h Handler) *Parser {
	guard := &tokenGuard{r: r}
	dec := xml.NewDecoder(guard)
	dec.Strict = true
	return &Parser{dec: dec, guard: guard, loc: loc, types: types, h: h,
		Skipped: map[string]int{}, Sources: map[string]*SourceInfo{}}
}

func (p *Parser) Run(stop func() bool) error {
	for n := 0; ; n++ {
		if n&0x3ff == 0 && stop != nil && stop() {
			return errCanceled
		}
		tok, err := p.token()
		if errors.Is(err, io.EOF) {
			if p.depth != 0 {
				return &ParseError{Offset: p.dec.InputOffset(), Err: errors.New("unexpected end of file")}
			}
			return nil
		}
		if err != nil {
			return &ParseError{Offset: p.dec.InputOffset(), Err: err}
		}
		switch t := tok.(type) {
		case xml.StartElement:
			p.depth++
			if p.depth != 2 {
				continue
			}
			switch t.Name.Local {
			case "Record":
				err = p.record(t)
			case "Workout":
				err = p.workout(t)
			case "ActivitySummary":
				p.summary(t)
				err = p.skip()
			case "ExportDate":
				if ms, _, ok := parseDate(attr(t, "value"), p.loc); ok {
					p.h.ExportDate(ms)
				}
				err = p.skip()
			case "Me":
				p.h.Me(Me{DOB: attr(t, "HKCharacteristicTypeIdentifierDateOfBirth"), Sex: attr(t, "HKCharacteristicTypeIdentifierBiologicalSex")})
				err = p.skip()
			default:
				err = p.skip()
			}
			if err != nil {
				return err
			}
		case xml.EndElement:
			p.depth--
		}
	}
}

var errCanceled = errors.New("import canceled")

// token reads the next token and marks its end for the token size limit.
func (p *Parser) token() (xml.Token, error) {
	tok, err := p.dec.RawToken()
	p.guard.mark = p.dec.InputOffset()
	return tok, err
}

// maxProblems caps the malformed-element details kept for the import summary.
const maxProblems = 20

// reject counts a malformed element and keeps its details for the first few.
func (p *Parser) reject(typ, reason string) {
	p.Skipped[typ]++
	if len(p.Problems) < maxProblems {
		p.Problems = append(p.Problems, fmt.Sprintf("%s near byte %d: %s", typ, p.dec.InputOffset(), reason))
	}
}

// skip consumes tokens until the element opened at the current depth is closed.
func (p *Parser) skip() error {
	target := p.depth - 1
	for p.depth > target {
		tok, err := p.token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = io.ErrUnexpectedEOF
			}
			return &ParseError{Offset: p.dec.InputOffset(), Err: err}
		}
		switch tok.(type) {
		case xml.StartElement:
			p.depth++
		case xml.EndElement:
			p.depth--
		}
	}
	return nil
}

// children calls fn for each direct child start element of the current element; fn may consume
// the child (returning true) or leave it to be skipped.
func (p *Parser) children(fn func(xml.StartElement) error) error {
	base := p.depth
	for {
		tok, err := p.token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = io.ErrUnexpectedEOF
			}
			return &ParseError{Offset: p.dec.InputOffset(), Err: err}
		}
		switch t := tok.(type) {
		case xml.StartElement:
			p.depth++
			if err := fn(t); err != nil {
				return err
			}
			if p.depth > base {
				if err := p.skip(); err != nil {
					return err
				}
			}
		case xml.EndElement:
			p.depth--
			if p.depth < base {
				return nil
			}
		}
	}
}

func (p *Parser) record(t xml.StartElement) error {
	typ := attr(t, "type")
	if !p.types.wanted(typ) {
		p.Ignored++
		return p.skip()
	}
	r := &Record{Type: typ}
	var start, end, value, device string
	for _, a := range t.Attr {
		switch a.Name.Local {
		case "sourceName":
			r.Source = a.Value
		case "unit":
			r.Unit = a.Value
		case "startDate":
			start = a.Value
		case "endDate":
			end = a.Value
		case "value":
			value = a.Value
		case "device":
			device = a.Value
		}
	}
	keepMeta := !metadataSkipTypes[typ] && !p.types.cumulative[typ]
	err := p.children(func(c xml.StartElement) error {
		if keepMeta && c.Name.Local == "MetadataEntry" && len(r.Meta) < maxChildren {
			r.Meta = append(r.Meta, [2]string{attr(c, "key"), attr(c, "value")})
		}
		return nil
	})
	if err != nil {
		return err
	}

	var ok bool
	if r.StartMS, r.Off, ok = parseDate(start, p.loc); !ok {
		p.reject(typ, fmt.Sprintf("invalid startDate %q", start))
		return nil
	}
	if r.EndMS, _, ok = parseDate(end, p.loc); !ok || r.EndMS < r.StartMS {
		p.reject(typ, fmt.Sprintf("invalid or early endDate %q", end))
		return nil
	}
	if r.Source == "" {
		p.reject(typ, "missing sourceName")
		return nil
	}
	if r.Unit == "" && strings.HasPrefix(typ, "HKCategoryTypeIdentifier") {
		// category records: the value is an enum identifier
		r.Category = value
	} else {
		v, err := strconv.ParseFloat(value, 64)
		if err != nil {
			p.reject(typ, fmt.Sprintf("invalid value %q", value))
			return nil
		}
		r.Unit, r.Value = normalize(typ, r.Unit, v)
		r.HasValue = true
	}
	r.SourceKind = p.noteSource(r.Source, device, r.StartMS)
	p.h.Record(r)
	return nil
}

func (p *Parser) workout(t xml.StartElement) error {
	w := &Workout{Meta: map[string]string{}}
	var start, end, device string
	var totalDist, totalDistUnit, totalEnergy, totalEnergyUnit, duration, durationUnit string
	for _, a := range t.Attr {
		switch a.Name.Local {
		case "workoutActivityType":
			w.ActivityType = a.Value
		case "sourceName":
			w.Source = a.Value
		case "startDate":
			start = a.Value
		case "endDate":
			end = a.Value
		case "device":
			device = a.Value
		case "duration":
			duration = a.Value
		case "durationUnit":
			durationUnit = a.Value
		case "totalDistance":
			totalDist = a.Value
		case "totalDistanceUnit":
			totalDistUnit = a.Value
		case "totalEnergyBurned":
			totalEnergy = a.Value
		case "totalEnergyBurnedUnit":
			totalEnergyUnit = a.Value
		}
	}
	err := p.children(func(c xml.StartElement) error {
		switch c.Name.Local {
		case "MetadataEntry":
			if len(w.Meta) < maxChildren {
				w.Meta[attr(c, "key")] = attr(c, "value")
			}
		case "WorkoutEvent":
			if ev, ok := p.workoutEvent(c); ok && len(w.Events) < maxChildren {
				w.Events = append(w.Events, ev)
			}
		case "WorkoutStatistics":
			if st, ok := p.workoutStat(c); ok && len(w.Stats) < maxChildren {
				w.Stats = append(w.Stats, st)
			}
		case "WorkoutActivity":
			return p.workoutActivity(c, w)
		case "WorkoutRoute":
			return p.children(func(rc xml.StartElement) error {
				if rc.Name.Local == "FileReference" && len(w.RoutePaths) < maxRouteFiles {
					if path := attr(rc, "path"); path != "" {
						w.RoutePaths = append(w.RoutePaths, path)
					}
				}
				return nil
			})
		}
		return nil
	})
	if err != nil {
		return err
	}

	var ok bool
	if w.StartMS, w.Off, ok = parseDate(start, p.loc); !ok || w.ActivityType == "" || w.Source == "" {
		p.reject("Workout", fmt.Sprintf("invalid startDate %q, type or source", start))
		return nil
	}
	if w.EndMS, _, ok = parseDate(end, p.loc); !ok || w.EndMS < w.StartMS {
		p.reject("Workout", fmt.Sprintf("invalid or early endDate %q", end))
		return nil
	}
	if v, ok := parseQty("", duration, durationUnit); ok {
		if durationUnit == "" {
			v *= 60 // HealthKit's default workout duration unit is minutes
		}
		w.DurationS = &v
	} else {
		v := float64(w.EndMS-w.StartMS) / 1000
		w.DurationS = &v
	}
	if v, ok := parseQty("", totalDist, totalDistUnit); ok && v > 0 {
		w.DistanceM = &v
	}
	if v, ok := parseQty("", totalEnergy, totalEnergyUnit); ok && v > 0 {
		w.EnergyKcal = &v
	}
	for _, st := range w.Stats {
		switch {
		case st.Type == TypeActiveEnergy && st.Sum != nil && w.EnergyKcal == nil:
			w.EnergyKcal = st.Sum
		case strings.HasPrefix(st.Type, "HKQuantityTypeIdentifierDistance") && st.Sum != nil && w.DistanceM == nil && *st.Sum > 0:
			w.DistanceM = st.Sum
		}
	}
	if v, ok := w.Meta["HKIndoorWorkout"]; ok {
		b := v == "1" || strings.EqualFold(v, "true")
		w.Indoor = &b
	}
	w.SourceKind = p.noteSource(w.Source, device, w.StartMS)
	p.h.Workout(w)
	return nil
}

func (p *Parser) workoutEvent(c xml.StartElement) (WorkoutEvent, bool) {
	ev := WorkoutEvent{Type: attr(c, "type")}
	ms, _, ok := parseDate(attr(c, "date"), p.loc)
	if !ok || ev.Type == "" {
		return ev, false
	}
	ev.StartMS = ms
	if d, ok := parseQty("", attr(c, "duration"), attr(c, "durationUnit")); ok && d > 0 {
		if attr(c, "durationUnit") == "" {
			d *= 60
		}
		ev.EndMS = ms + int64(d*1000)
	}
	return ev, true
}

func (p *Parser) workoutStat(c xml.StartElement) (WorkoutStat, bool) {
	st := WorkoutStat{Type: attr(c, "type")}
	if st.Type == "" {
		return st, false
	}
	unit := attr(c, "unit")
	for _, a := range c.Attr {
		var dst **float64
		switch a.Name.Local {
		case "sum":
			dst = &st.Sum
		case "average":
			dst = &st.Avg
		case "minimum":
			dst = &st.Min
		case "maximum":
			dst = &st.Max
		case "startDate":
			st.StartMS, _, _ = parseDate(a.Value, p.loc)
			continue
		case "endDate":
			st.EndMS, _, _ = parseDate(a.Value, p.loc)
			continue
		default:
			continue
		}
		if v, ok := parseQty(st.Type, a.Value, unit); ok {
			*dst = &v
		}
	}
	st.Unit, _ = normalize(st.Type, unit, 0)
	return st, true
}

func (p *Parser) workoutActivity(c xml.StartElement, w *Workout) error {
	wa := WorkoutActivity{Type: attr(c, "workoutActivityType")}
	if wa.Type == "" {
		wa.Type = w.ActivityType
	}
	wa.StartMS, _, _ = parseDate(attr(c, "startDate"), p.loc)
	wa.EndMS, _, _ = parseDate(attr(c, "endDate"), p.loc)
	err := p.children(func(cc xml.StartElement) error {
		switch cc.Name.Local {
		case "WorkoutStatistics":
			if st, ok := p.workoutStat(cc); ok && len(wa.Stats) < maxChildren {
				wa.Stats = append(wa.Stats, st)
			}
		case "WorkoutEvent":
			if ev, ok := p.workoutEvent(cc); ok && len(w.Events) < maxChildren {
				w.Events = append(w.Events, ev)
			}
		}
		return nil
	})
	if err == nil && wa.StartMS > 0 && wa.EndMS >= wa.StartMS && len(w.Activities) < maxChildren {
		w.Activities = append(w.Activities, wa)
	}
	return err
}

func (p *Parser) summary(t xml.StartElement) {
	s := &Summary{}
	var energyUnit string
	vals := map[string]*float64{}
	for _, a := range t.Attr {
		switch a.Name.Local {
		case "dateComponents":
			s.Day = a.Value
		case "activeEnergyBurnedUnit":
			energyUnit = a.Value
		default:
			if v, err := strconv.ParseFloat(a.Value, 64); err == nil {
				vals[a.Name.Local] = &v
			}
		}
	}
	if len(s.Day) != 10 {
		p.reject("ActivitySummary", fmt.Sprintf("invalid dateComponents %q", s.Day))
		return
	}
	conv := func(v *float64) *float64 {
		if v == nil {
			return nil
		}
		_, n := normalize("", energyUnit, *v)
		if energyUnit == "" {
			n = *v
		}
		return &n
	}
	s.ActiveKcal = conv(vals["activeEnergyBurned"])
	s.ActiveKcalGoal = conv(vals["activeEnergyBurnedGoal"])
	s.MoveMin = vals["appleMoveTime"]
	s.MoveMinGoal = vals["appleMoveTimeGoal"]
	s.ExerciseMin = vals["appleExerciseTime"]
	s.ExerciseMinGoal = vals["appleExerciseTimeGoal"]
	s.StandHours = vals["appleStandHours"]
	s.StandHoursGoal = vals["appleStandHoursGoal"]
	p.h.Summary(s)
}

func (p *Parser) noteSource(name, device string, ms int64) string {
	si, ok := p.Sources[name]
	if !ok {
		si = &SourceInfo{Name: name, Kind: sourceKind(name, device), FirstMS: ms, LastMS: ms}
		p.Sources[name] = si
		return si.Kind
	}
	if si.Kind == "other" && device != "" {
		si.Kind = sourceKind(name, device)
	}
	if ms < si.FirstMS {
		si.FirstMS = ms
	}
	if ms > si.LastMS {
		si.LastMS = ms
	}
	return si.Kind
}

// sourceKind classifies a source by its HKDevice model, falling back to its name.
func sourceKind(name, device string) string {
	switch {
	case strings.Contains(device, "model:Watch"):
		return "watch"
	case strings.Contains(device, "model:iPhone"):
		return "phone"
	case strings.Contains(name, "Watch"):
		return "watch"
	case strings.Contains(name, "iPhone"):
		return "phone"
	}
	return "other"
}

func parseQty(typ, value, unit string) (float64, bool) {
	if value == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, false
	}
	if unit == "" {
		return v, true
	}
	_, v = normalize(typ, unit, v)
	return v, true
}

func attr(t xml.StartElement, name string) string {
	for _, a := range t.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}
