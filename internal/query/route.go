package query

import (
	"context"
	"database/sql"
	"errors"
	"math"

	"ritm/internal/ingest"
)

// RoutePoint is one point of a downsampled workout route. Pace covers the stretch since the
// previous returned point; it is absent while stopped or without timestamps.
type RoutePoint struct {
	Lat        float64  `json:"lat"`
	Lon        float64  `json:"lon"`
	EleM       float64  `json:"ele_m"`
	TMS        int64    `json:"t_ms"`
	PaceSPerKm *float64 `json:"pace_s_per_km"`
}

type RouteBounds struct {
	MinLat float64 `json:"min_lat"`
	MinLon float64 `json:"min_lon"`
	MaxLat float64 `json:"max_lat"`
	MaxLon float64 `json:"max_lon"`
}

// RouteSummary describes a route without its points (used by MCP).
type RouteSummary struct {
	Points         int         `json:"points"`
	DistanceM      float64     `json:"distance_m"`
	ElevationGainM float64     `json:"elevation_gain_m"`
	StartMS        int64       `json:"start_ms"`
	EndMS          int64       `json:"end_ms"`
	Bounds         RouteBounds `json:"bounds"`
}

type Route struct {
	RouteSummary
	Track []RoutePoint `json:"track"`
}

// minMovingMps is the speed below which a stretch counts as stopped (no pace).
const minMovingMps = 0.5

// eleHysteresisM ignores elevation wiggles smaller than this when summing the climb (GPS noise).
const eleHysteresisM = 3

// Route returns the workout's GPS route downsampled to about maxPoints, or ErrNotFound when the
// workout has no stored route.
func (s *Service) Route(ctx context.Context, workoutID int64, maxPoints int) (*Route, error) {
	var blob []byte
	err := s.db.RO.QueryRowContext(ctx, "SELECT points FROM workout_routes WHERE workout_id = ?", workoutID).Scan(&blob)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	pts, err := ingest.DecodeRoute(blob)
	if err != nil {
		return nil, err
	}
	if len(pts) == 0 {
		return nil, ErrNotFound
	}
	return buildRoute(pts, maxPoints), nil
}

func buildRoute(pts []ingest.RoutePoint, maxPoints int) *Route {
	r := &Route{RouteSummary: RouteSummary{Points: len(pts), StartMS: pts[0].TimeMS, EndMS: pts[len(pts)-1].TimeMS,
		Bounds: RouteBounds{MinLat: pts[0].Lat, MinLon: pts[0].Lon, MaxLat: pts[0].Lat, MaxLon: pts[0].Lon}}}
	// cum[i] is the distance from the start to point i.
	cum := make([]float64, len(pts))
	low := pts[0].EleM
	for i, p := range pts {
		r.Bounds.MinLat, r.Bounds.MaxLat = min(r.Bounds.MinLat, p.Lat), max(r.Bounds.MaxLat, p.Lat)
		r.Bounds.MinLon, r.Bounds.MaxLon = min(r.Bounds.MinLon, p.Lon), max(r.Bounds.MaxLon, p.Lon)
		if i > 0 {
			cum[i] = cum[i-1] + haversineM(pts[i-1], p)
		}
		switch {
		case p.EleM < low:
			low = p.EleM
		case p.EleM-low >= eleHysteresisM:
			r.ElevationGainM += p.EleM - low
			low = p.EleM
		}
	}
	r.DistanceM = math.Round(cum[len(cum)-1])
	r.ElevationGainM = math.Round(r.ElevationGainM)

	if maxPoints < 2 {
		maxPoints = 500
	}
	step := max(1, (len(pts)+maxPoints-1)/maxPoints)
	prev := -1
	for i := 0; i < len(pts); i += step {
		r.Track = append(r.Track, point(pts, cum, prev, i))
		prev = i
	}
	if last := len(pts) - 1; prev != last {
		r.Track = append(r.Track, point(pts, cum, prev, last))
	}
	return r
}

func point(pts []ingest.RoutePoint, cum []float64, prev, i int) RoutePoint {
	p := pts[i]
	out := RoutePoint{Lat: p.Lat, Lon: p.Lon, EleM: math.Round(p.EleM*10) / 10, TMS: p.TimeMS}
	if prev >= 0 {
		dt := float64(p.TimeMS-pts[prev].TimeMS) / 1000
		if d := cum[i] - cum[prev]; dt > 0 && d/dt >= minMovingMps {
			pace := math.Round(dt / (d / 1000))
			out.PaceSPerKm = &pace
		}
	}
	return out
}

func haversineM(a, b ingest.RoutePoint) float64 {
	const earthR = 6_371_000.0
	rad := math.Pi / 180
	dLat, dLon := (b.Lat-a.Lat)*rad, (b.Lon-a.Lon)*rad
	h := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(a.Lat*rad)*math.Cos(b.Lat*rad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * earthR * math.Asin(math.Min(1, math.Sqrt(h)))
}
