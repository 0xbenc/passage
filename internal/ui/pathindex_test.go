package ui

import (
	"reflect"
	"testing"
)

var fixturePaths = []string{
	"pp/alter-ego/proton",
	"pp/backup/key",
	"work/aws/db",
}

func TestBuildPathIndexFolders(t *testing.T) {
	idx := buildPathIndex(fixturePaths)
	for _, f := range []string{"pp", "pp/alter-ego", "pp/backup", "work", "work/aws"} {
		if _, ok := idx.folders[f]; !ok {
			t.Errorf("expected folder %q in index", f)
		}
	}
	// Leaves are entries, not folders.
	for _, notFolder := range []string{"pp/alter-ego/proton", "pp/backup/key", "work/aws/db"} {
		if _, ok := idx.folders[notFolder]; ok {
			t.Errorf("%q should be an entry, not a folder", notFolder)
		}
		if _, ok := idx.entries[notFolder]; !ok {
			t.Errorf("expected entry %q in index", notFolder)
		}
	}
}

func TestBuildPathIndexChildrenSortedFoldersFirst(t *testing.T) {
	idx := buildPathIndex([]string{"pp/zeta/x", "pp/alpha", "pp/beta/y"})
	got := idx.children[""]
	want := []pathNode{{Name: "pp", IsFolder: true}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("root children = %#v, want %#v", got, want)
	}
	// Within pp: folders (beta, zeta) before the entry (alpha), each alpha-sorted.
	ppc := idx.children["pp"]
	wantPP := []pathNode{
		{Name: "beta", IsFolder: true},
		{Name: "zeta", IsFolder: true},
		{Name: "alpha", IsEntry: true},
	}
	if !reflect.DeepEqual(ppc, wantPP) {
		t.Fatalf("pp children = %#v, want %#v", ppc, wantPP)
	}
}

func TestBuildPathIndexFileFolderCollision(t *testing.T) {
	// "a/b" is both an entry and a folder (because "a/b/c" nests under it).
	idx := buildPathIndex([]string{"a/b", "a/b/c"})
	ac := idx.children["a"]
	if len(ac) != 1 {
		t.Fatalf("a children = %#v, want one node", ac)
	}
	if !ac[0].IsFolder || !ac[0].IsEntry || ac[0].Name != "b" {
		t.Fatalf("a/b should be folder AND entry: %#v", ac[0])
	}
	// Classification precedence: exact entry wins over folder.
	if k := idx.classifyLeaf("a/b"); k != leafExistingEntry {
		t.Fatalf("classifyLeaf(a/b) = %v, want leafExistingEntry", k)
	}
}

func TestBuildPathIndexEmpty(t *testing.T) {
	for _, in := range [][]string{nil, {}, {"", "  ", "/"}} {
		idx := buildPathIndex(in)
		if len(idx.entries) != 0 || len(idx.folders) != 0 || len(idx.children) != 0 {
			t.Fatalf("empty input %#v built non-empty index", in)
		}
		if k := idx.classifyLeaf("anything/new"); k != leafNew {
			t.Fatalf("empty store: classifyLeaf = %v, want leafNew", k)
		}
	}
}

func TestSplitPath(t *testing.T) {
	cases := []struct{ in, dir, frag string }{
		{"", "", ""},
		{"p", "", "p"},
		{"pp/", "pp/", ""},
		{"pp/al", "pp/", "al"},
		{"pp/alter-ego/gm", "pp/alter-ego/", "gm"},
		{"/x", "/", "x"},
	}
	for _, c := range cases {
		dir, frag := splitPath(c.in)
		if dir != c.dir || frag != c.frag {
			t.Errorf("splitPath(%q) = (%q,%q), want (%q,%q)", c.in, dir, frag, c.dir, c.frag)
		}
	}
}

func TestCompletionCandidates(t *testing.T) {
	idx := buildPathIndex(fixturePaths)

	// Empty frag at root: both folders, all matches, no highlight.
	all := idx.completionCandidates("", "")
	if len(all) != 2 {
		t.Fatalf("root candidates = %d, want 2", len(all))
	}
	for _, c := range all {
		if !c.match || c.positions != nil {
			t.Fatalf("empty frag candidate should match with no positions: %#v", c)
		}
	}

	// Prefix "p" at root: pp matches (highlighted), work dimmed, matches first.
	got := idx.completionCandidates("", "p")
	if got[0].node.Name != "pp" || !got[0].match {
		t.Fatalf("first candidate = %#v, want matching pp", got[0])
	}
	if !reflect.DeepEqual(got[0].positions, []int{0}) {
		t.Fatalf("pp positions = %#v, want [0]", got[0].positions)
	}
	if got[1].node.Name != "work" || got[1].match {
		t.Fatalf("second candidate = %#v, want non-matching work", got[1])
	}

	// Descended into pp: children alter-ego, backup.
	sub := idx.completionCandidates("pp/", "")
	names := []string{sub[0].node.Name, sub[1].node.Name}
	if !reflect.DeepEqual(names, []string{"alter-ego", "backup"}) {
		t.Fatalf("pp/ children = %v, want [alter-ego backup]", names)
	}

	// Case-insensitive prefix.
	ci := idx.completionCandidates("", "WOR")
	if mc := matchingCandidates(ci); len(mc) != 1 || mc[0].node.Name != "work" {
		t.Fatalf("case-insensitive WOR should match work: %#v", mc)
	}
}

func TestCommonCompletionPrefix(t *testing.T) {
	idx := buildPathIndex([]string{"x/alpha/a", "x/alto/b", "x/beta/c"})
	cands := idx.completionCandidates("x/", "al")
	if got := commonCompletionPrefix(cands); got != "al" {
		t.Fatalf("common prefix of alpha,alto = %q, want al", got)
	}
	// Single match completes fully.
	if got := commonCompletionPrefix(idx.completionCandidates("x/", "be")); got != "beta" {
		t.Fatalf("single-match prefix = %q, want beta", got)
	}
	// No match -> empty.
	if got := commonCompletionPrefix(idx.completionCandidates("x/", "zzz")); got != "" {
		t.Fatalf("no-match prefix = %q, want empty", got)
	}
	// Canonical case emitted from the stored spelling.
	ci := buildPathIndex([]string{"Apple", "Apricot"})
	if got := commonCompletionPrefix(ci.completionCandidates("", "ap")); got != "Ap" {
		t.Fatalf("canonical-case prefix = %q, want Ap", got)
	}
}

func TestClassifyLeaf(t *testing.T) {
	idx := buildPathIndex([]string{"pp/alter-ego/proton", "Work/github"})
	cases := []struct {
		field string
		want  leafKind
	}{
		{"", leafEmpty},
		{"   ", leafEmpty},
		{"pp/", leafTrailingSlash},
		{"pp//x", leafEmptySegment},
		{"/x", leafEmptySegment},
		{"pp", leafPathIsFolder},
		{"pp/alter-ego", leafPathIsFolder},
		{"pp/alter-ego/proton", leafExistingEntry},
		{"work/github", leafCaseCollision}, // stored as Work/github
		{"Work/github", leafExistingEntry}, // exact
		{"pp/alter-ego/gmail", leafNew},
	}
	for _, c := range cases {
		if got := idx.classifyLeaf(c.field); got != c.want {
			t.Errorf("classifyLeaf(%q) = %v, want %v", c.field, got, c.want)
		}
	}
	if canon := idx.caseCollisionCanonical("work/github"); canon != "Work/github" {
		t.Errorf("caseCollisionCanonical = %q, want Work/github", canon)
	}
}

func TestApplyNode(t *testing.T) {
	f, descended := applyNode("pp/", pathNode{Name: "alter-ego", IsFolder: true})
	if f != "pp/alter-ego/" || !descended {
		t.Fatalf("folder apply = (%q,%v), want (pp/alter-ego/,true)", f, descended)
	}
	f, descended = applyNode("pp/alter-ego/", pathNode{Name: "proton", IsEntry: true})
	if f != "pp/alter-ego/proton" || descended {
		t.Fatalf("entry apply = (%q,%v), want (pp/alter-ego/proton,false)", f, descended)
	}
	// A name that is both a folder and an entry descends (folder wins).
	f, descended = applyNode("a/", pathNode{Name: "b", IsFolder: true, IsEntry: true})
	if f != "a/b/" || !descended {
		t.Fatalf("dual node apply = (%q,%v), want (a/b/,true)", f, descended)
	}
}

func TestBreadcrumbSegments(t *testing.T) {
	idx := buildPathIndex(fixturePaths)
	segs, leaf, kind := idx.breadcrumbSegments("pp/alter-ego/gmail")
	want := []breadcrumbSeg{{Name: "pp", Exists: true}, {Name: "alter-ego", Exists: true}}
	if !reflect.DeepEqual(segs, want) {
		t.Fatalf("segs = %#v, want %#v", segs, want)
	}
	if leaf != "gmail" || kind != leafNew {
		t.Fatalf("leaf=%q kind=%v, want gmail/leafNew", leaf, kind)
	}
	// A typed folder that does not exist is flagged.
	segs, _, _ = idx.breadcrumbSegments("pp/ghost/x")
	if len(segs) != 2 || segs[1].Name != "ghost" || segs[1].Exists {
		t.Fatalf("ghost folder should be flagged missing: %#v", segs)
	}
	// No committed folder yet.
	segs, leaf, _ = idx.breadcrumbSegments("pp")
	if segs != nil || leaf != "pp" {
		t.Fatalf("root leaf: segs=%#v leaf=%q, want nil/pp", segs, leaf)
	}
}
