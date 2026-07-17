package dedup

import (
	"sort"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
)

// naiveTrigramPairs is the reference implementation: it compares every
// unordered pair (the original O(n^2) scan) using the same trigram extraction
// and Jaccard scoring as the production path, so the inverted-index scan can be
// proved to yield an identical candidate set.
func naiveTrigramPairs(rule Rule, entities, values []string, dismissed map[string]bool) []Candidate {
	out := []Candidate{}
	add := func(ai, bi int, score float64) {
		a, b := canonicalPair(entities[ai], entities[bi])
		if dismissed[a+"\x00"+b] {
			return
		}
		va, vb := values[ai], values[bi]
		if a != entities[ai] {
			va, vb = vb, va
		}
		out = append(out, Candidate{EntityA: a, EntityB: b, ValueA: va, ValueB: vb, Score: score})
	}
	grams := make([][]string, len(values))
	for idx, v := range values {
		grams[idx] = trigrams(v)
	}
	for x := 0; x < len(values); x++ {
		for y := x + 1; y < len(values); y++ {
			if score := jaccard(grams[x], grams[y]); score >= rule.Threshold {
				add(x, y, score)
			}
		}
	}
	return out
}

func sortCandidates(c []Candidate) {
	sort.SliceStable(c, func(a, b int) bool {
		if c[a].EntityA != c[b].EntityA {
			return c[a].EntityA < c[b].EntityA
		}
		return c[a].EntityB < c[b].EntityB
	})
}

func TestTrigramInvertedIndexEquivalence(t *testing.T) {
	Convey("Given a fixture of entity values with overlapping and disjoint names", t, func() {
		entities := []string{"e1", "e2", "e3", "e4", "e5", "e6", "e7"}
		values := []string{
			"Trail Bike 500",
			"trail bike 500",    // near-identical to e1
			"Trail Bike 5000",   // close to e1/e2
			"Road Helmet XL",    // disjoint from the bikes
			"Road Helmet XXL",   // close to e4
			"!!!",               // content-free, no trigrams
			"Mountain Bike Pro", // shares "bike" trigrams with e1-e3
		}
		rule := Rule{Strategy: StrategyTrigram, Threshold: 0.3}

		Convey("When matched by the inverted-index scan and the naive all-pairs reference", func() {
			got, truncated := matchPairs(rule, entities, values, map[string]bool{}, time.Time{}, time.Now)
			want := naiveTrigramPairs(rule, entities, values, map[string]bool{})
			sortCandidates(got)
			sortCandidates(want)

			Convey("Then the two produce an identical candidate set", func() {
				So(truncated, ShouldBeFalse)
				So(got, ShouldResemble, want)
			})
		})

		Convey("When the threshold is varied across the full range", func() {
			Convey("Then the inverted-index scan equals the reference at every threshold", func() {
				for _, th := range []float64{0.1, 0.25, 0.5, 0.75, 1.0} {
					r := Rule{Strategy: StrategyTrigram, Threshold: th}
					got, _ := matchPairs(r, entities, values, map[string]bool{}, time.Time{}, time.Now)
					want := naiveTrigramPairs(r, entities, values, map[string]bool{})
					sortCandidates(got)
					sortCandidates(want)
					So(got, ShouldResemble, want)
				}
			})
		})

		Convey("When the soft time budget is already exceeded", func() {
			// A deadline in the past forces the first budget check to bail out.
			past := time.Now().Add(-time.Hour)
			_, truncated := matchPairs(rule, entities, values, map[string]bool{}, past, time.Now)

			Convey("Then the scan reports itself truncated rather than silently capping", func() {
				So(truncated, ShouldBeTrue)
			})
		})
	})
}
