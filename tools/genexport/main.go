// Command genexport writes a synthetic Apple Health export.zip for development and benchmarks.
//
//	go run ./tools/genexport -days 180 -o testdata/synthetic.zip
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"ritm/internal/synth"
)

func main() {
	out := flag.String("o", "synthetic-export.zip", "output zip path")
	n := flag.Int("days", 120, "number of days")
	end := flag.String("end", "", "last day (YYYY-MM-DD, default yesterday)")
	hr := flag.Int("hr", 300, "passive heart-rate interval in seconds (lower = bigger export)")
	seed := flag.Uint64("seed", 1, "random seed")
	flag.Parse()

	loc, _ := time.LoadLocation("Europe/Kyiv")
	last := time.Now().In(loc).AddDate(0, 0, -1)
	if *end != "" {
		t, err := time.ParseInLocation("2006-01-02", *end, loc)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		last = t
	}
	start := time.Date(last.Year(), last.Month(), last.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -*n+1)
	o := synth.Options{Start: start, Days: *n, Loc: loc, Seed: *seed, Sleep: true, IPhone: true, HRIntervalSec: *hr,
		// a slower second half to give the insights engine something to say
		StepScale: func(d int) float64 {
			if d > *n-10 {
				return 0.75
			}
			return 1
		}}
	if err := synth.WriteZip(*out, o); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fi, _ := os.Stat(*out)
	fmt.Printf("wrote %s (%d days, %.1f MB)\n", *out, *n, float64(fi.Size())/1e6)
}
