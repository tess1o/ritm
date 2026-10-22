package ingest

import (
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
)

type RoutePoint struct {
	Lat    float64 `json:"lat"`
	Lon    float64 `json:"lon"`
	EleM   float64 `json:"ele_m"`
	TimeMS int64   `json:"t_ms"`
}

// parseGPX reads trkpt elements from a workout route GPX file.
func parseGPX(r io.Reader) ([]RoutePoint, error) {
	dec := xml.NewDecoder(r)
	var pts []RoutePoint
	var cur *RoutePoint
	var field string
	for {
		tok, err := dec.RawToken()
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
