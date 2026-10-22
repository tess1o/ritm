package derive

import (
	"context"
	"errors"
	"testing"

	"ritm/internal/days"
)

// A failed recompute leaves its start day pending; the next post-import recompute, even with
// nothing new imported, starts from there and clears it.
func TestFailedRecomputeIsRepaired(t *testing.T) {
	ctx := context.Background()
	_, eng := setup(t, opts())
	fail := true
	var from []days.Day
	eng.OnRecomputed(func(_ context.Context, d days.Day, _ *Resolved) error {
		from = append(from, d)
		if fail {
			return errors.New("boom")
		}
		return nil
	})
	if err := eng.Recompute(ctx, "2026-01-20"); err == nil {
		t.Fatal("expected the recompute to fail")
	}
	if p, err := eng.HasPending(ctx); err != nil || !p {
		t.Fatalf("pending = %v (%v), want true", p, err)
	}

	// A later failure starting after the pending day keeps the earlier day.
	if err := eng.Recompute(ctx, "2026-01-25"); err == nil {
		t.Fatal("expected the recompute to fail")
	}

	fail = false
	if err := eng.AfterImport(ctx, ""); err != nil {
		t.Fatal(err)
	}
	want := days.MustParse("2026-01-20")
	if got := from[len(from)-1]; got != want {
		t.Fatalf("repair recomputed from %s, want %s (calls %v)", got, want, from)
	}
	if p, _ := eng.HasPending(ctx); p {
		t.Fatal("pending marker not cleared")
	}

	// Nothing new and nothing pending: no recompute.
	n := len(from)
	if err := eng.AfterImport(ctx, ""); err != nil || len(from) != n {
		t.Fatalf("unexpected recompute: %v %v", err, from[n:])
	}
}
