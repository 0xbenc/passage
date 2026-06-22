package fuzzy

import (
	"reflect"
	"testing"
)

func TestMatchPositionsAndOK(t *testing.T) {
	cases := []struct {
		name      string
		query     string
		candidate string
		wantOK    bool
		wantPos   []int
	}{
		{"delimiter boundary", "gh", "work/github/token", true, []int{5, 8}},
		{"empty query matches", "", "anything", true, nil},
		{"no match", "xyz", "work/github", false, nil},
		{"subsequence not contiguous", "wgt", "work/github/token", true, []int{0, 5, 7}},
		{"multibyte candidate", "本", "日本語", true, []int{1}},
		{"multibyte subsequence", "日語", "日本語", true, []int{0, 2}},
		{"longer than candidate", "abcd", "abc", false, nil},
		{"exact", "abc", "abc", true, []int{0, 1, 2}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, ok := Match(tc.query, tc.candidate)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && !reflect.DeepEqual(res.Positions, tc.wantPos) {
				t.Fatalf("positions = %v, want %v", res.Positions, tc.wantPos)
			}
		})
	}
}

func TestSmartCase(t *testing.T) {
	// Lowercase query is case-insensitive.
	if _, ok := Match("api", "my-API-key"); !ok {
		t.Fatal("lowercase query should match uppercase text case-insensitively")
	}
	// Any uppercase makes the whole query case-sensitive.
	if _, ok := Match("API", "my-api-key"); ok {
		t.Fatal("uppercase query must not match lowercase text (smart-case)")
	}
	if res, ok := Match("API", "my-API-key"); !ok || !reflect.DeepEqual(res.Positions, []int{3, 4, 5}) {
		t.Fatalf("uppercase query against matching case: ok=%v pos=%v", ok, res.Positions)
	}
}

func TestBoundaryOutranksMidWord(t *testing.T) {
	boundary, ok1 := Match("ho", "host/x")
	mid, ok2 := Match("ho", "xhoy")
	if !ok1 || !ok2 {
		t.Fatalf("both should match: %v %v", ok1, ok2)
	}
	if boundary.Score <= mid.Score {
		t.Fatalf("boundary score %d should exceed mid-word score %d", boundary.Score, mid.Score)
	}
}

func TestConsecutiveOutranksSplit(t *testing.T) {
	contiguous, _ := Match("gh", "ghx")
	split, _ := Match("gh", "gxh")
	if contiguous.Score <= split.Score {
		t.Fatalf("contiguous score %d should exceed split score %d", contiguous.Score, split.Score)
	}
}

func TestRelevanceGate(t *testing.T) {
	// Scattered subsequence: relevant=false. Contiguous/boundary: relevant=true.
	scattered, _ := Match("redis", "occpw | internet | midwest0 | wifi-eth | password")
	if Relevant(scattered, len("redis")) {
		t.Fatalf("scattered redis (score %d) should not be relevant", scattered.Score)
	}
	for _, tc := range []struct{ q, c string }{
		{"redis", "occdev | proxmox | mdw0 | vms | redis | password"},
		{"gh", "work | github | token"},
		{"occredis", "occdev | proxmox | mdw0 | vms | redis | password"},
	} {
		r, _ := Match(tc.q, tc.c)
		if !Relevant(r, len([]rune(tc.q))) {
			t.Fatalf("legit match %q in %q (score %d) should be relevant", tc.q, tc.c, r.Score)
		}
	}
	// Empty query is always relevant.
	if !Relevant(Result{}, 0) {
		t.Fatal("empty query should be relevant")
	}
}

func TestEarlierBoundaryRankingForHost(t *testing.T) {
	// A realistic ranking expectation: "gh" prefers the github host whose
	// match sits right after a delimiter over an incidental scatter.
	strong, _ := Match("gh", "work/github/token")
	weak, _ := Match("gh", "graph-thing")
	if strong.Score <= weak.Score {
		t.Fatalf("delimiter-boundary match %d should beat scattered match %d", strong.Score, weak.Score)
	}
}
