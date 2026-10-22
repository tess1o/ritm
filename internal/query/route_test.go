package query

import (
	"math"
	"testing"

	"ritm/internal/ingest"
)

// A straight walk north at 1 m/s with a point per second, a 10 m climb with 1 m GPS jitter,
// and a 60 s stop in the middle.
func walk() []ingest.RoutePoint {
	const mPerDegLat = 111_195.0
	var pts []ingest.RoutePoint
	t, dist := int64(0), 0.0
	for i := 0; i < 1000; i++ {
		if i == 500 {
			t += 60_000 // stopped: time passes, position does not
		}
		jitter := 0.0
		if i%2 == 1 {
			jitter = 1
		}
		pts = append(pts, ingest.RoutePoint{Lat: 50 + dist/mPerDegLat, Lon: 30, EleM: 100 + 10*float64(i)/999 + jitter, TimeMS: t})
		t += 1000
		dist++
	}
	return pts
}

func TestBuildRoute(t *testing.T) {
	r := buildRoute(walk(), 100)
	if math.Abs(r.DistanceM-999) > 2 {
		t.Errorf("distance %v, want ~999 m", r.DistanceM)
	}
	if r.ElevationGainM < 8 || r.ElevationGainM > 12 {
		t.Errorf("elevation gain %v, want ~10 m (jitter ignored)", r.ElevationGainM)
	}
	if r.Points != 1000 || len(r.Track) < 95 || len(r.Track) > 101 {
		t.Errorf("points %d, track %d", r.Points, len(r.Track))
	}
	if last := r.Track[len(r.Track)-1]; last.TMS != r.EndMS {
		t.Errorf("last track point %d, want the route end %d", last.TMS, r.EndMS)
	}
	if r.Track[0].PaceSPerKm != nil {
		t.Error("first point has a pace")
	}
	var paces []float64
	for _, p := range r.Track[1:] {
		if p.PaceSPerKm != nil {
			paces = append(paces, *p.PaceSPerKm)
		}
	}
	// 1 m/s = 1000 s/km everywhere except the stretch containing the stop.
	slow := 0
	for _, p := range paces {
		if p > 1100 {
			slow++
		} else if math.Abs(p-1000) > 5 {
			t.Errorf("pace %v, want ~1000 s/km", p)
		}
	}
	if slow > 1 || len(paces) < len(r.Track)-3 {
		t.Errorf("paces %v", paces)
	}
	if r.Bounds.MinLat != 50 || r.Bounds.MaxLat <= r.Bounds.MinLat || r.Bounds.MinLon != 30 {
		t.Errorf("bounds %+v", r.Bounds)
	}
}
