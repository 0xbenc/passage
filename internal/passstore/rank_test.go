package passstore

import (
	"reflect"
	"testing"
)

func rankedPaths(entries []Entry, filter string, mfaOnly bool) []string {
	out := []string{}
	for _, r := range Rank(entries, filter, mfaOnly) {
		out = append(out, entries[r.Index].Path)
	}
	return out
}

func TestRankEmptyFilterPreservesOrder(t *testing.T) {
	entries := []Entry{
		{Path: "a", Display: Display("a")},
		{Path: "b", Display: Display("b")},
		{Path: "c", Display: Display("c")},
	}
	if got := rankedPaths(entries, "", false); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("empty-filter order = %v, want store order", got)
	}
}

func TestRankMFAOnlyFilters(t *testing.T) {
	entries := []Entry{
		{Path: "a", Display: Display("a"), HasMFA: true},
		{Path: "b", Display: Display("b")},
	}
	if got := rankedPaths(entries, "", true); !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatalf("mfaOnly = %v, want only a", got)
	}
}

func TestRankFuzzyCrossesDelimiter(t *testing.T) {
	entries := []Entry{
		{Path: "work/github/token", Display: Display("work/github/token")},
		{Path: "work/aws/key", Display: Display("work/aws/key")},
	}
	if got := rankedPaths(entries, "gh", false); !reflect.DeepEqual(got, []string{"work/github/token"}) {
		t.Fatalf("fuzzy gh = %v, want only github", got)
	}
}

func TestRankPinnedFirst(t *testing.T) {
	entries := []Entry{
		{Path: "alpha/site", Display: Display("alpha/site")},
		{Path: "beta/site", Display: Display("beta/site"), Pinned: true},
	}
	got := rankedPaths(entries, "site", false)
	if len(got) != 2 || got[0] != "beta/site" {
		t.Fatalf("ranked = %v, want pinned beta/site first", got)
	}
}

func TestRankRecencyTiebreak(t *testing.T) {
	entries := []Entry{
		{Path: "a/site", Display: Display("a/site"), LastUsed: 100},
		{Path: "b/site", Display: Display("b/site"), LastUsed: 200},
	}
	// Equal score (symmetric "site" match) -> more recent first.
	got := rankedPaths(entries, "site", false)
	if got[0] != "b/site" {
		t.Fatalf("ranked = %v, want more-recent b/site first", got)
	}
}

func TestRankPositionsIndexDisplay(t *testing.T) {
	entries := []Entry{{Path: "work/github", Display: Display("work/github")}}
	res := Rank(entries, "gh", false)
	if len(res) != 1 {
		t.Fatalf("want 1 match, got %d", len(res))
	}
	disp := []rune(entries[0].Display) // "work | github"
	matched := ""
	for _, p := range res[0].Positions {
		if p < 0 || p >= len(disp) {
			t.Fatalf("position %d out of range for %q", p, entries[0].Display)
		}
		matched += string(disp[p])
	}
	if matched != "gh" {
		t.Fatalf("matched runes = %q, want gh", matched)
	}
}
