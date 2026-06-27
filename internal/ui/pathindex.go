package ui

import (
	"sort"
	"strings"
	"unicode"
)

// pathNode is one immediate child of a folder in the implicit pass tree. A name
// can be both a folder and an entry at once (an "a/b" entry alongside an
// "a/b/c" entry), so the two flags are independent.
type pathNode struct {
	Name     string
	IsFolder bool
	IsEntry  bool
}

// pathIndex is an immutable, point-in-time snapshot of the store's entry paths,
// precomputed into the lookups the new-entry path completer needs. Folders in
// pass are implicit — there is no folder entity — so every leading path segment
// is treated as a folder. All fields are read-only after construction so a
// composerModel (copied by value on every keystroke) can share it safely.
type pathIndex struct {
	entries     map[string]struct{} // exact full entry paths
	entriesFold map[string]string   // lower(path) -> canonical path, for case-collision detection
	folders     map[string]struct{} // every leading-prefix folder (no trailing slash): "pp", "pp/alter-ego"
	children    map[string][]pathNode
}

// buildPathIndex derives the implicit folder tree from a snapshot of entry
// paths. It is pure and total: a nil/empty input yields an index whose lookups
// all miss and whose children are empty (the empty-store case).
func buildPathIndex(entryPaths []string) pathIndex {
	idx := pathIndex{
		entries:     map[string]struct{}{},
		entriesFold: map[string]string{},
		folders:     map[string]struct{}{},
		children:    map[string][]pathNode{},
	}
	type agg struct{ isFolder, isEntry bool }
	childAgg := map[string]map[string]*agg{}
	addChild := func(dir, name string, isFolder bool) {
		m := childAgg[dir]
		if m == nil {
			m = map[string]*agg{}
			childAgg[dir] = m
		}
		a := m[name]
		if a == nil {
			a = &agg{}
			m[name] = a
		}
		if isFolder {
			a.isFolder = true
		} else {
			a.isEntry = true
		}
	}
	for _, raw := range entryPaths {
		p := strings.Trim(strings.TrimSpace(raw), "/")
		if p == "" {
			continue
		}
		if _, ok := idx.entries[p]; !ok {
			idx.entries[p] = struct{}{}
			if lower := strings.ToLower(p); idx.entriesFold[lower] == "" {
				idx.entriesFold[lower] = p
			}
		}
		parts := strings.Split(p, "/")
		for i, part := range parts {
			if part == "" { // defensive: a malformed "a//b" snapshot
				continue
			}
			parent := strings.Join(parts[:i], "/")
			isFolder := i < len(parts)-1
			addChild(parent, part, isFolder)
			if isFolder {
				idx.folders[strings.Join(parts[:i+1], "/")] = struct{}{}
			}
		}
	}
	for dir, m := range childAgg {
		nodes := make([]pathNode, 0, len(m))
		for name, a := range m {
			nodes = append(nodes, pathNode{Name: name, IsFolder: a.isFolder, IsEntry: a.isEntry})
		}
		sortNodes(nodes)
		idx.children[dir] = nodes
	}
	return idx
}

// sortNodes orders children folders-first, then by name. A name that is both a
// folder and an entry sorts as a folder.
func sortNodes(nodes []pathNode) {
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].IsFolder != nodes[j].IsFolder {
			return nodes[i].IsFolder
		}
		return nodes[i].Name < nodes[j].Name
	})
}

// splitPath splits a path field into (dir, frag): dir is the text up to and
// including the last "/" ("" when there is none); frag is the active segment
// after it. "pp/al" -> ("pp/","al"); "pp" -> ("","pp"); "pp/" -> ("pp/","").
func splitPath(field string) (dir, frag string) {
	if i := strings.LastIndex(field, "/"); i >= 0 {
		return field[:i+1], field[i+1:]
	}
	return "", field
}

// folderKey turns a dir (with trailing slash, as splitPath returns) into the
// children-map key (no trailing slash; "" for root).
func folderKey(dir string) string {
	return strings.TrimSuffix(dir, "/")
}

// pathCandidate is one child of the current folder, tagged with whether it
// prefix-matches the active fragment (case-insensitively) and, if so, the rune
// positions to highlight in its name.
type pathCandidate struct {
	node      pathNode
	match     bool
	positions []int
}

// completionCandidates returns the children of dir, matches first (preserving
// folder-first/name order within each group). Matching is case-insensitive
// prefix — the same lockstep relationship TAB uses — so everything listed as a
// match is TAB-completable.
func (idx pathIndex) completionCandidates(dir, frag string) []pathCandidate {
	children := idx.children[folderKey(dir)]
	out := make([]pathCandidate, 0, len(children))
	fragLower := strings.ToLower(frag)
	fragRunes := len([]rune(frag))
	for _, n := range children {
		match := fragLower == "" || strings.HasPrefix(strings.ToLower(n.Name), fragLower)
		c := pathCandidate{node: n, match: match}
		if match && fragRunes > 0 {
			pos := make([]int, fragRunes)
			for i := range pos {
				pos[i] = i
			}
			c.positions = pos
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].match != out[j].match {
			return out[i].match
		}
		return false
	})
	return out
}

// matchingCandidates is the subset of completionCandidates that prefix-match —
// the set TAB acts on.
func matchingCandidates(cands []pathCandidate) []pathCandidate {
	out := make([]pathCandidate, 0, len(cands))
	for _, c := range cands {
		if c.match {
			out = append(out, c)
		}
	}
	return out
}

// commonCompletionPrefix is the longest common prefix of the matching candidate
// names, compared case-insensitively but emitted in the first match's canonical
// case — true shell first-TAB behavior.
func commonCompletionPrefix(cands []pathCandidate) string {
	var first []rune
	var common []rune
	have := false
	for _, c := range cands {
		if !c.match {
			continue
		}
		nr := []rune(c.node.Name)
		if !have {
			first = nr
			common = nr
			have = true
			continue
		}
		n := min(len(common), len(nr))
		k := 0
		for k < n && foldEqualRune(common[k], nr[k]) {
			k++
		}
		common = common[:k]
	}
	if !have {
		return ""
	}
	return string(first[:len(common)])
}

// applyNode fills dir+node into a path field: a folder becomes "dir/name/" (and
// descends); an entry becomes "dir/name". A name that is both descends, since
// the user can always backspace to target the entry.
func applyNode(dir string, n pathNode) (field string, descended bool) {
	if n.IsFolder {
		return dir + n.Name + "/", true
	}
	return dir + n.Name, false
}

// leafKind classifies a typed path for enter-validation and breadcrumb valence.
type leafKind int

const (
	leafEmpty         leafKind = iota // nothing typed
	leafTrailingSlash                 // ends with "/", no name yet
	leafEmptySegment                  // "a//b" or "/a"
	leafPathIsFolder                  // exact existing folder, no trailing name
	leafExistingEntry                 // exact existing entry (overwrite)
	leafCaseCollision                 // matches an existing entry only by case
	leafNew                           // a fresh entry to create
)

// classifyLeaf is the single source of truth for whether enter may proceed and
// for the leaf's valence. Precedence: exact entry > exact folder > case
// collision > new.
func (idx pathIndex) classifyLeaf(field string) leafKind {
	f := strings.TrimSpace(field)
	switch {
	case f == "":
		return leafEmpty
	case strings.HasSuffix(f, "/"):
		return leafTrailingSlash
	}
	for _, seg := range strings.Split(f, "/") {
		if seg == "" {
			return leafEmptySegment
		}
	}
	if _, ok := idx.entries[f]; ok {
		return leafExistingEntry
	}
	if _, ok := idx.folders[f]; ok {
		return leafPathIsFolder
	}
	if canon, ok := idx.entriesFold[strings.ToLower(f)]; ok && canon != f {
		return leafCaseCollision
	}
	return leafNew
}

// caseCollisionCanonical returns the canonical (stored) spelling of an entry
// that the field collides with only by case, for the notice message.
func (idx pathIndex) caseCollisionCanonical(field string) string {
	return idx.entriesFold[strings.ToLower(strings.TrimSpace(field))]
}

// breadcrumbSeg is one committed folder segment of the path for the read-only
// "in" line, with a case-insensitive existence flag.
type breadcrumbSeg struct {
	Name   string
	Exists bool
}

// breadcrumbSegments returns the committed folder segments (everything before
// the last "/") with existence flags, plus the active leaf and its kind — so
// render can color accent=exists / warning=missing-or-overwrite / muted=new
// without splicing into the cursored field.
func (idx pathIndex) breadcrumbSegments(field string) (segs []breadcrumbSeg, leaf string, kind leafKind) {
	kind = idx.classifyLeaf(field)
	dir, frag := splitPath(field)
	leaf = frag
	key := folderKey(dir)
	if key == "" {
		return nil, leaf, kind
	}
	parts := strings.Split(key, "/")
	for i := range parts {
		if parts[i] == "" {
			continue
		}
		prefix := strings.Join(parts[:i+1], "/")
		_, exists := idx.folders[prefix]
		segs = append(segs, breadcrumbSeg{Name: parts[i], Exists: exists})
	}
	return segs, leaf, kind
}

func foldEqualRune(a, b rune) bool {
	return a == b || unicode.ToLower(a) == unicode.ToLower(b)
}

// ascendPath drops the trailing path segment, for shift+tab: "pp/alter-ego/gm"
// -> "pp/alter-ego/" -> "pp/" -> "". A trailing slash is removed first.
func ascendPath(field string) string {
	field = strings.TrimSuffix(field, "/")
	if i := strings.LastIndex(field, "/"); i >= 0 {
		return field[:i+1]
	}
	return ""
}

// displayDir names a dir (with trailing slash, as splitPath returns) for a
// notice; the root reads as "the store root".
func displayDir(dir string) string {
	if folderKey(dir) == "" {
		return "the store root"
	}
	return folderKey(dir)
}

// childPath is the full path of a child name under dir (with trailing slash),
// used to look up a folder's grandchildren for its item count.
func childPath(dir, name string) string {
	if k := folderKey(dir); k != "" {
		return k + "/" + name
	}
	return name
}
