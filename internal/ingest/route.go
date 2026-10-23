package ingest

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"ritm/internal/store"
)

type RoutePoint struct {
	Lat    float64 `json:"lat"`
	Lon    float64 `json:"lon"`
	EleM   float64 `json:"ele_m"`
	TimeMS int64   `json:"t_ms"`
}

var errTooManyPoints = fmt.Errorf("more than %d route points", maxRoutePoints)

// parseGPX reads trkpt elements from a workout route GPX file, at most limit of them.
func parseGPX(r io.Reader, limit int) ([]RoutePoint, error) {
	guard := &tokenGuard{r: r}
	dec := xml.NewDecoder(guard)
	var pts []RoutePoint
	var cur *RoutePoint
	var field string
	for {
		tok, err := dec.RawToken()
		guard.mark = dec.InputOffset()
		if errors.Is(err, io.EOF) {
			return pts, nil
		}
		if err != nil {
			return pts, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "trkpt":
				p := RoutePoint{}
				p.Lat, _ = strconv.ParseFloat(attr(t, "lat"), 64)
				p.Lon, _ = strconv.ParseFloat(attr(t, "lon"), 64)
				cur = &p
			case "ele", "time":
				field = t.Name.Local
			}
		case xml.CharData:
			if cur == nil || field == "" {
				continue
			}
			s := strings.TrimSpace(string(t))
			switch field {
			case "ele":
				cur.EleM, _ = strconv.ParseFloat(s, 64)
			case "time":
				if ts, err := time.Parse(time.RFC3339, s); err == nil {
					cur.TimeMS = ts.UnixMilli()
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "trkpt":
				if cur != nil {
					if len(pts) == limit {
						return pts, errTooManyPoints
					}
					pts = append(pts, *cur)
					cur = nil
				}
			case "ele", "time":
				field = ""
			}
		}
	}
}

const routeFormatV1 = 1

// EncodeRoute packs points as: version byte, then per point lat/lon float64, ele float32, time int64 (LE).
func EncodeRoute(pts []RoutePoint) []byte {
	var buf bytes.Buffer
	buf.WriteByte(routeFormatV1)
	b := make([]byte, 28)
	for _, p := range pts {
		binary.LittleEndian.PutUint64(b[0:], math.Float64bits(p.Lat))
		binary.LittleEndian.PutUint64(b[8:], math.Float64bits(p.Lon))
		binary.LittleEndian.PutUint32(b[16:], math.Float32bits(float32(p.EleM)))
		binary.LittleEndian.PutUint64(b[20:], uint64(p.TimeMS))
		buf.Write(b)
	}
	return buf.Bytes()
}

func DecodeRoute(b []byte) ([]RoutePoint, error) {
	if len(b) == 0 || b[0] != routeFormatV1 || (len(b)-1)%28 != 0 {
		return nil, errors.New("unknown route encoding")
	}
	b = b[1:]
	pts := make([]RoutePoint, 0, len(b)/28)
	for i := 0; i+28 <= len(b); i += 28 {
		pts = append(pts, RoutePoint{
			Lat:    math.Float64frombits(binary.LittleEndian.Uint64(b[i:])),
			Lon:    math.Float64frombits(binary.LittleEndian.Uint64(b[i+8:])),
			EleM:   float64(math.Float32frombits(binary.LittleEndian.Uint32(b[i+16:]))),
			TimeMS: int64(binary.LittleEndian.Uint64(b[i+20:])),
		})
	}
	return pts, nil
}

// RouteLocation is where a route starts and ends and the area it covers, as stored next to the
// points (workout_routes) so lists can place and filter workouts without decoding them.
type RouteLocation struct {
	Points             int
	StartLat, StartLon float64
	EndLat, EndLon     float64
	MinLat, MinLon     float64
	MaxLat, MaxLon     float64
}

// Locate summarises pts, which must not be empty.
func Locate(pts []RoutePoint) RouteLocation {
	first, last := pts[0], pts[len(pts)-1]
	l := RouteLocation{Points: len(pts), StartLat: first.Lat, StartLon: first.Lon, EndLat: last.Lat, EndLon: last.Lon,
		MinLat: first.Lat, MinLon: first.Lon, MaxLat: first.Lat, MaxLon: first.Lon}
	for _, p := range pts {
		l.MinLat, l.MaxLat = min(l.MinLat, p.Lat), max(l.MaxLat, p.Lat)
		l.MinLon, l.MaxLon = min(l.MinLon, p.Lon), max(l.MaxLon, p.Lon)
	}
	return l
}

// saveRoute stores a workout's points with their location summary.
func saveRoute(ctx context.Context, tx *sql.Tx, workoutID int64, pts []RoutePoint) error {
	l := Locate(pts)
	_, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO workout_routes (workout_id, points, point_count,
		start_lat, start_lon, end_lat, end_lon, min_lat, min_lon, max_lat, max_lon) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		workoutID, EncodeRoute(pts), l.Points, l.StartLat, l.StartLon, l.EndLat, l.EndLon, l.MinLat, l.MinLon, l.MaxLat, l.MaxLon)
	if err != nil {
		return err
	}
	return store.BumpDataVersion(ctx, tx)
}

// BackfillRouteLocations fills the location summary of routes stored before it existed. It
// does nothing once every route has one.
func BackfillRouteLocations(ctx context.Context, db *store.DB) (int, error) {
	rows, err := db.W.QueryContext(ctx, "SELECT workout_id, points FROM workout_routes WHERE point_count IS NULL")
	if err != nil {
		return 0, err
	}
	type route struct {
		id  int64
		pts []RoutePoint
	}
	var todo []route
	for rows.Next() {
		var r route
		var blob []byte
		if err := rows.Scan(&r.id, &blob); err != nil {
			rows.Close()
			return 0, err
		}
		if r.pts, err = DecodeRoute(blob); err == nil && len(r.pts) > 0 {
			todo = append(todo, r)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(todo) == 0 {
		return 0, err
	}
	err = store.InTx(ctx, db.W, func(tx *sql.Tx) error {
		for _, r := range todo {
			l := Locate(r.pts)
			if _, err := tx.ExecContext(ctx, `UPDATE workout_routes SET point_count = ?, start_lat = ?, start_lon = ?, end_lat = ?,
				end_lon = ?, min_lat = ?, min_lon = ?, max_lat = ?, max_lon = ? WHERE workout_id = ?`,
				l.Points, l.StartLat, l.StartLon, l.EndLat, l.EndLon, l.MinLat, l.MinLon, l.MaxLat, l.MaxLon, r.id); err != nil {
				return err
			}
		}
		return store.BumpDataVersion(ctx, tx)
	})
	return len(todo), err
}
