// Package mathx holds the small numeric helpers shared by the derive, insights, query, monitor
// and MCP packages.
package mathx

import (
	"math"
	"sort"
)

// Round rounds v to digits decimal places.
func Round(v float64, digits int) float64 {
	p := math.Pow(10, float64(digits))
	return math.Round(v*p) / p
}

// RoundPtr rounds v, or returns nil when v is NaN or infinite (a missing value in JSON).
func RoundPtr(v float64, digits int) *float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return nil
	}
	r := Round(v, digits)
	return &r
}

// Mean returns the arithmetic mean, or NaN for no values.
func Mean(v []float64) float64 {
	if len(v) == 0 {
		return math.NaN()
	}
	var s float64
	for _, x := range v {
		s += x
	}
	return s / float64(len(v))
}

// Median returns the median without modifying v, or NaN for no values.
func Median(v []float64) float64 {
	if len(v) == 0 {
		return math.NaN()
	}
	c := append([]float64(nil), v...)
	sort.Float64s(c)
	n := len(c)
	if n%2 == 1 {
		return c[n/2]
	}
	return (c[n/2-1] + c[n/2]) / 2
}

// Stdev is the sample standard deviation (0 for fewer than two values).
func Stdev(v []float64) float64 {
	if len(v) < 2 {
		return 0
	}
	m := Mean(v)
	var s float64
	for _, x := range v {
		s += (x - m) * (x - m)
	}
	return math.Sqrt(s / float64(len(v)-1))
}

// MinMaxMean returns the minimum, maximum and mean (zeros for no values).
func MinMaxMean(v []float64) (lo, hi, mean float64) {
	if len(v) == 0 {
		return 0, 0, 0
	}
	lo, hi = math.Inf(1), math.Inf(-1)
	var sum float64
	for _, x := range v {
		lo, hi = math.Min(lo, x), math.Max(hi, x)
		sum += x
	}
	return lo, hi, sum / float64(len(v))
}
