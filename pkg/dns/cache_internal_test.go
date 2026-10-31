package dns

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

// scriptRandInt returns a deterministic randInt function consuming the given
// draws in order; each draw is taken modulo the caller's bound. It returns 0
// once the script is exhausted.
func scriptRandInt(draws ...int64) func(int64) int64 {
	i := 0
	return func(n int64) int64 {
		if i >= len(draws) {
			return 0
		}
		d := draws[i] % n
		i++
		return d
	}
}

func srvPriorities(recs []*SRV) []uint16 {
	prios := make([]uint16, 0, len(recs))
	for _, rec := range recs {
		if rec == nil {
			continue
		}
		prios = append(prios, rec.Priority)
	}
	return prios
}

func srvTargetList(recs []*SRV) []string {
	targets := make([]string, 0, len(recs))
	for _, rec := range recs {
		if rec == nil {
			targets = append(targets, "<nil>")
			continue
		}
		targets = append(targets, rec.Target)
	}
	return targets
}

func TestWeightedSRVOrder_PrioritiesGrouped(t *testing.T) {
	t.Parallel()

	recs := []*SRV{
		{Target: "a", Priority: 20, Weight: 5},
		{Target: "b", Priority: 10, Weight: 1},
		{Target: "c", Priority: 10, Weight: 2},
		{Target: "d", Priority: 20, Weight: 0},
		{Target: "e", Priority: 5, Weight: 1},
	}

	got := weightedSRVOrder(recs, scriptRandInt(0))

	if diff := cmp.Diff([]uint16{5, 10, 10, 20, 20}, srvPriorities(got)); diff != "" {
		t.Errorf("weightedSRVOrder() priorities mismatch (-want +got):\n%s", diff)
	}
	// randInt == 0 deterministically picks: for {b(1), c(2)} the shuffle swaps
	// them then a zero draw takes the first; for {a(5), d(0)} the zero-weight
	// record is placed first and selected by the zero draw.
	if diff := cmp.Diff([]string{"e", "c", "b", "d", "a"}, srvTargetList(got)); diff != "" {
		t.Errorf("weightedSRVOrder() targets mismatch (-want +got):\n%s", diff)
	}
}

func TestWeightedSRVOrder_WeightedDraw(t *testing.T) {
	t.Parallel()

	newRecs := func() []*SRV {
		return []*SRV{
			{Target: "a", Priority: 10, Weight: 1},
			{Target: "b", Priority: 10, Weight: 5},
		}
	}

	// Shuffle draw 0 swaps the pair to [b(5), a(1)]; a selection draw of 5
	// meets b's cumulative weight first.
	got := weightedSRVOrder(newRecs(), scriptRandInt(0, 5))
	if diff := cmp.Diff([]string{"b", "a"}, srvTargetList(got)); diff != "" {
		t.Errorf("weightedSRVOrder() targets mismatch (-want +got):\n%s", diff)
	}

	// The same shuffled order with a draw of 6 exceeds b's cumulative weight
	// and selects a: the draw, not the input order, drives the selection.
	got = weightedSRVOrder(newRecs(), scriptRandInt(0, 6))
	if diff := cmp.Diff([]string{"a", "b"}, srvTargetList(got)); diff != "" {
		t.Errorf("weightedSRVOrder() targets mismatch (-want +got):\n%s", diff)
	}
}

func TestWeightedSRVOrder_ZeroWeightSelectedByZeroDraw(t *testing.T) {
	t.Parallel()

	newRecs := func() []*SRV {
		return []*SRV{
			{Target: "z", Priority: 10, Weight: 0},
			{Target: "a", Priority: 10, Weight: 5},
		}
	}

	// After shuffling to [a, z] the zero-weight record is placed first and a
	// draw of 0 gives it the RFC 2782 chance of being selected.
	got := weightedSRVOrder(newRecs(), scriptRandInt(0, 0))
	if diff := cmp.Diff([]string{"z", "a"}, srvTargetList(got)); diff != "" {
		t.Errorf("weightedSRVOrder() targets mismatch (-want +got):\n%s", diff)
	}

	// A non-zero draw skips the zero-weight candidate.
	got = weightedSRVOrder(newRecs(), scriptRandInt(0, 1))
	if diff := cmp.Diff([]string{"a", "z"}, srvTargetList(got)); diff != "" {
		t.Errorf("weightedSRVOrder() targets mismatch (-want +got):\n%s", diff)
	}
}

func TestWeightedSRVOrder_AllZeroGroupRandomized(t *testing.T) {
	t.Parallel()

	recs := []*SRV{
		{Target: "a", Priority: 10, Weight: 0},
		{Target: "b", Priority: 10, Weight: 0},
		{Target: "c", Priority: 10, Weight: 0},
	}

	// With all weights zero every draw is 0 and candidates are taken in the
	// shuffled order rather than freezing to the backend order.
	got := weightedSRVOrder(recs, scriptRandInt(0, 0))
	if diff := cmp.Diff([]string{"b", "c", "a"}, srvTargetList(got)); diff != "" {
		t.Errorf("weightedSRVOrder() targets mismatch (-want +got):\n%s", diff)
	}
}

func TestWeightedSRVOrder_ZerosFirstPartition(t *testing.T) {
	t.Parallel()

	// Three positive records of differing weights plus two zero-weight
	// records exercise the zero/non-zero partition, inclusive draw
	// boundaries, and selection removal.
	recs := []*SRV{
		{Target: "a", Priority: 10, Weight: 0},
		{Target: "b", Priority: 10, Weight: 3},
		{Target: "c", Priority: 10, Weight: 0},
		{Target: "d", Priority: 10, Weight: 1},
		{Target: "e", Priority: 10, Weight: 2},
	}

	// The first four draws shuffle [a,b,c,d,e] to [d,c,a,b,e]; the zero-weight
	// partition then yields [c,a,d,b,e] with each partition's relative order
	// preserved. Selection draws 0,1,5,2 pick c (zero draw hits the first
	// zero), d (cumulative 1 >= 1), e (cumulative 5 >= 5), and b.
	got := weightedSRVOrder(recs, scriptRandInt(4, 1, 0, 0, 0, 1, 5, 2))
	if diff := cmp.Diff([]string{"c", "d", "e", "b", "a"}, srvTargetList(got)); diff != "" {
		t.Errorf("weightedSRVOrder() targets mismatch (-want +got):\n%s", diff)
	}
}

func TestWeightedSRVOrder_RemovesOnlyPicked(t *testing.T) {
	t.Parallel()

	recs := []*SRV{
		{Target: "a", Priority: 10, Weight: 1},
		{Target: "b", Priority: 10, Weight: 2},
		{Target: "c", Priority: 10, Weight: 3},
	}

	// Shuffling with zero draws yields [b(2), c(3), a(1)]; a draw of 4 picks c
	// (cumulative 2 < 4 <= 5), and the remaining [b, a] keep their order so the
	// next draw of 2 picks b.
	got := weightedSRVOrder(recs, scriptRandInt(0, 0, 4, 2))
	if diff := cmp.Diff([]string{"c", "b", "a"}, srvTargetList(got)); diff != "" {
		t.Errorf("weightedSRVOrder() targets mismatch (-want +got):\n%s", diff)
	}
}

func TestWeightedSRVOrder_EdgeCases(t *testing.T) {
	t.Parallel()

	if got := weightedSRVOrder(nil, scriptRandInt()); got != nil {
		t.Errorf("weightedSRVOrder(nil) = %v, want nil", got)
	}
	if got := weightedSRVOrder([]*SRV{}, scriptRandInt()); got == nil || len(got) != 0 {
		t.Errorf("weightedSRVOrder([]) = %v, want non-nil empty slice", got)
	}

	single := &SRV{Target: "a", Priority: 10, Weight: 7}
	got := weightedSRVOrder([]*SRV{single}, scriptRandInt())
	if len(got) != 1 || got[0] != single {
		t.Errorf("weightedSRVOrder(single) = %v, want [%v]", got, single)
	}

	got = weightedSRVOrder([]*SRV{nil, {Target: "a", Priority: 10}, nil}, scriptRandInt())
	if diff := cmp.Diff([]string{"a", "<nil>", "<nil>"}, srvTargetList(got)); diff != "" {
		t.Errorf("weightedSRVOrder() with nil entries mismatch (-want +got):\n%s", diff)
	}
}

func TestWeightedSRVOrder_InputNotMutated(t *testing.T) {
	t.Parallel()

	recs := []*SRV{
		{Target: "a", Priority: 20, Weight: 5},
		{Target: "b", Priority: 10, Weight: 2},
		{Target: "c", Priority: 10, Weight: 1},
	}
	want := []*SRV{
		{Target: "a", Priority: 20, Weight: 5},
		{Target: "b", Priority: 10, Weight: 2},
		{Target: "c", Priority: 10, Weight: 1},
	}

	got := weightedSRVOrder(recs, scriptRandInt(2, 1, 0))
	if len(got) != len(recs) {
		t.Fatalf("weightedSRVOrder() len = %d, want %d", len(got), len(recs))
	}

	if diff := cmp.Diff(want, recs); diff != "" {
		t.Errorf("input slice mutated (-want +got):\n%s", diff)
	}
}
