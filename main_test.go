package main

import (
	"sort"
	"testing"
)

func TestParseName(t *testing.T) {
	cases := []struct {
		name       string
		raw        string
		forceExact bool
		want       matcher
	}{
		{"bare input is substring", "notepad", false, matcher{pattern: "notepad"}},
		{"input is lowercased", "NoTePaD", false, matcher{pattern: "notepad"}},
		{".exe suffix is exact", "notepad.exe", false, matcher{exact: "notepad.exe"}},
		{".exe suffix is lowercased", "NOTEPAD.EXE", false, matcher{exact: "notepad.exe"}},
		{"explicit star forces substring", "*notepad.exe", false, matcher{pattern: "notepad"}},
		{"star is stripped", "*team*", false, matcher{pattern: "team"}},
		{"star and .exe are stripped", "*TEAM*.exe", false, matcher{pattern: "team"}},
		{"forced exact keeps .exe", "steam.exe", true, matcher{exact: "steam.exe"}},
		{"forced exact without .exe gets alt name", "system", true, matcher{exact: "system.exe", exactAlt: "system"}},
	}
	for _, c := range cases {
		if got := parseName(c.raw, c.forceExact); got != c.want {
			t.Errorf("%s: parseName(%q, %v) = %+v, want %+v", c.name, c.raw, c.forceExact, got, c.want)
		}
	}
}

func TestMatcherMatches(t *testing.T) {
	exact := parseName("steam.exe", false)
	if !exact.matches("Steam.exe") {
		t.Error("exact matcher must be case-insensitive")
	}
	if exact.matches("steam_helper.exe") {
		t.Error("exact matcher must not match a longer name")
	}

	alt := parseName("System", true)
	if !alt.matches("System") {
		t.Error("forced exact without .exe must match the extensionless pseudo-process")
	}
	if !alt.matches("SYSTEM.EXE") {
		t.Error("forced exact without .exe must also match <name>.exe")
	}
	if alt.matches("System Idle Process") {
		t.Error("forced exact must not match by substring")
	}

	sub := parseName("*team*", false)
	if !sub.matches("ms-teams.exe") {
		t.Error("fuzzy matcher must match anywhere in the name")
	}
	if !sub.matches("steam.exe") {
		t.Error("fuzzy matcher matches substrings anywhere, including inside steam.exe")
	}
	if sub.matches("firefox.exe") {
		t.Error("fuzzy matcher must not match an unrelated name")
	}
}

func TestFormatCPU(t *testing.T) {
	const sec = 10000000
	cases := []struct {
		ticks uint64
		want  string
	}{
		{0, "00:00:00"},
		{59 * sec, "00:00:59"},
		{3661 * sec, "01:01:01"},
		{86399 * sec, "23:59:59"},
		{86400 * sec, "1:00:00:00"},
		{90061 * sec, "1:01:01:01"},
	}
	for _, c := range cases {
		if got := formatCPU(c.ticks); got != c.want {
			t.Errorf("formatCPU(%d) = %q, want %q", c.ticks, got, c.want)
		}
	}
}

func nameMatcher(name string) func(string) bool {
	m := parseName(name, false)
	return m.matches
}

func matchedPIDs(procs map[uint32]procInfo, matches func(string) bool) []uint32 {
	var out []uint32
	for pid, p := range procs {
		if matches(p.name) {
			out = append(out, pid)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func rowByRoot(rows []procRow, root uint32) (procRow, bool) {
	for _, r := range rows {
		if r.root == root {
			return r, true
		}
	}
	return procRow{}, false
}

func TestBuildKidsDropsStaleParentEdges(t *testing.T) {
	procs := map[uint32]procInfo{
		1:   {ppid: 0, name: "System", created: 100},
		100: {ppid: 1, name: "game.exe", created: 2000},
		// Reused PID: this process predates the PID it points at, so it is
		// not a child of 100.
		101: {ppid: 100, name: "orphan.exe", created: 1000},
	}
	kids := buildKids(procs)
	for _, pid := range kids[100] {
		if pid == 101 {
			t.Error("buildKids must drop an edge whose parent was created after the child")
		}
	}
	// A child created after its parent is a real edge.
	procs[102] = procInfo{ppid: 100, name: "real.exe", created: 3000}
	if kids := buildKids(procs); len(kids[100]) != 1 || kids[100][0] != 102 {
		t.Errorf("buildKids dropped a valid edge: %v", kids[100])
	}
}

func TestBuildRowsRollsUpAllDescendants(t *testing.T) {
	procs := map[uint32]procInfo{
		1:   {ppid: 0, name: "System", created: 100},
		100: {ppid: 1, name: "explorer.exe", created: 200, ws: 100, commit: 200, cpu: 5},
		101: {ppid: 100, name: "notepad.exe", created: 300, ws: 50, commit: 60, cpu: 1},
		102: {ppid: 100, name: "helper.exe", created: 400, ws: 10, commit: 20},
	}
	m := nameMatcher("explorer.exe")
	rows := buildRows(procs, matchedPIDs(procs, m), m, false, false, false)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1: %+v", len(rows), rows)
	}
	r := rows[0]
	if r.root != 100 || r.procs != 3 || r.ws != 160 || r.commit != 280 || r.cpu != 6 {
		t.Errorf("rollup = %+v, want root 100, 3 procs, ws 160, commit 280, cpu 6", r)
	}
	for _, want := range []string{"explorer.exe", "notepad.exe", "helper.exe"} {
		if !r.names[want] {
			t.Errorf("names missing %q: %v", want, r.names)
		}
	}
}

func TestBuildRowsStrictTreeTreatsForeignChildrenAsCutPoints(t *testing.T) {
	procs := map[uint32]procInfo{
		1:   {ppid: 0, name: "System", created: 100},
		100: {ppid: 1, name: "steam.exe", created: 200, ws: 100, commit: 200},
		101: {ppid: 100, name: "steam.exe", created: 300, ws: 50, commit: 60},
		102: {ppid: 100, name: "webhelper.exe", created: 400, ws: 10, commit: 20},
		103: {ppid: 102, name: "steam.exe", created: 500, ws: 5, commit: 5},
	}
	m := nameMatcher("steam.exe")
	rows := buildRows(procs, matchedPIDs(procs, m), m, false, true, false)
	// webhelper.exe is a cut point: it is neither counted nor traversed
	// through, so the steam.exe beneath it stays a root of its own.
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2 (root + grandchild behind the cut point): %+v", len(rows), rows)
	}
	r, ok := rowByRoot(rows, 100)
	if !ok {
		t.Fatalf("root 100 missing: %+v", rows)
	}
	if r.procs != 2 || r.ws != 150 {
		t.Errorf("/x rollup = %+v, want 2 procs (root + same-name child), ws 150", r)
	}
	if r.names["webhelper.exe"] {
		t.Error("/x must not count a differently-named child")
	}
	if len(r.names) != 1 || !r.names["steam.exe"] {
		t.Errorf("/x names = %v, want only steam.exe", r.names)
	}
	cut, ok := rowByRoot(rows, 103)
	if !ok {
		t.Fatalf("grandchild behind the cut point must form its own row: %+v", rows)
	}
	if cut.procs != 1 || cut.ws != 5 {
		t.Errorf("cut-point grandchild = %+v, want 1 proc, ws 5", cut)
	}
}

func TestBuildRowsPrunesNestedRoot(t *testing.T) {
	// a.exe -> mid.exe (non-matching) -> a.exe: the grandchild must be folded
	// into the topmost root instead of forming its own row.
	procs := map[uint32]procInfo{
		1:   {ppid: 0, name: "System", created: 100},
		400: {ppid: 1, name: "a.exe", created: 200, ws: 100, commit: 100},
		401: {ppid: 400, name: "mid.exe", created: 300, ws: 50, commit: 50},
		402: {ppid: 401, name: "a.exe", created: 400, ws: 10, commit: 10},
	}
	m := nameMatcher("a.exe")
	rows := buildRows(procs, matchedPIDs(procs, m), m, false, false, false)
	if len(rows) != 1 {
		t.Fatalf("nested matching grandchild was not pruned: %+v", rows)
	}
	r := rows[0]
	if r.root != 400 || r.procs != 3 || r.ws != 160 || r.commit != 160 {
		t.Errorf("pruned rollup = %+v, want root 400, 3 procs, ws/commit 160", r)
	}
}

func TestBuildRowsExcludesPIDReuseDescendants(t *testing.T) {
	procs := map[uint32]procInfo{
		1:   {ppid: 0, name: "System", created: 100},
		200: {ppid: 1, name: "game.exe", created: 2000, ws: 10, commit: 20},
		// Points at the root PID but was created before it existed: the PID
		// was recycled, so this is not a descendant.
		202: {ppid: 200, name: "helper.exe", created: 500, ws: 1000, commit: 2000},
		201: {ppid: 200, name: "game.exe", created: 3000, ws: 30, commit: 40},
	}
	m := nameMatcher("game.exe")
	rows := buildRows(procs, matchedPIDs(procs, m), m, false, false, false)
	root, ok := rowByRoot(rows, 200)
	if !ok {
		t.Fatalf("root 200 missing: %+v", rows)
	}
	if root.procs != 2 || root.ws != 40 {
		t.Errorf("root 200 = %+v, want 2 procs, ws 40 (stale PID-reuse edge must not count)", root)
	}
	if root.names["helper.exe"] {
		t.Error("a process created before its recorded parent must not be counted as its descendant")
	}
}

func TestBuildRowsFlatModeListsEachMatch(t *testing.T) {
	procs := map[uint32]procInfo{
		1:   {ppid: 0, name: "System", created: 100},
		400: {ppid: 1, name: "a.exe", created: 200, ws: 10, commit: 30},
		401: {ppid: 400, name: "mid.exe", created: 300, ws: 5, commit: 50},
		402: {ppid: 401, name: "a.exe", created: 400, ws: 1, commit: 20},
	}
	m := nameMatcher("a.exe")
	rows := buildRows(procs, matchedPIDs(procs, m), m, true, false, false)
	if len(rows) != 2 {
		t.Fatalf("flat mode got %d rows, want 2: %+v", len(rows), rows)
	}
	// Sorted by descending commit: 402 (20) before 400 (30)? commit 30 > 20 -> 400 first.
	if rows[0].root != 400 || rows[1].root != 402 {
		t.Errorf("flat order = %d,%d, want 400,402 (descending commit)", rows[0].root, rows[1].root)
	}
	for _, r := range rows {
		if r.procs != 1 {
			t.Errorf("flat row %d: procs = %d, want 1 (no rollup)", r.root, r.procs)
		}
	}
	if rows[0].ws != 10 || rows[0].commit != 30 {
		t.Errorf("flat row must report only its own process: %+v", rows[0])
	}
}

func TestBuildRowsShortModeSkipsNames(t *testing.T) {
	procs := map[uint32]procInfo{
		1:   {ppid: 0, name: "System", created: 100},
		100: {ppid: 1, name: "explorer.exe", created: 200, ws: 100, commit: 200},
	}
	m := nameMatcher("explorer.exe")
	rows := buildRows(procs, matchedPIDs(procs, m), m, false, false, true)
	if len(rows) != 1 || len(rows[0].names) != 0 {
		t.Errorf("/s tree mode must not collect names: %+v", rows)
	}

	rowsFlat := buildRows(procs, matchedPIDs(procs, m), m, true, false, true)
	if len(rowsFlat) != 1 || len(rowsFlat[0].names) != 0 {
		t.Errorf("/s flat mode must not collect names: %+v", rowsFlat)
	}
}

func TestRootOf(t *testing.T) {
	procs := map[uint32]procInfo{
		0:   {ppid: 0, name: "System Idle Process", created: 0},
		1:   {ppid: 0, name: "System", created: 100},
		100: {ppid: 1, name: "parent.exe", created: 200},
		101: {ppid: 100, name: "child.exe", created: 300},
		102: {ppid: 101, name: "grandchild.exe", created: 400},
		// Stale parent:
		200: {ppid: 1, name: "newparent.exe", created: 5000},
		201: {ppid: 200, name: "oldchild.exe", created: 1000},
	}

	mParent := nameMatcher("parent.exe")
	if r := rootOf(procs, 102, mParent); r != 102 {
		t.Errorf("rootOf grandchild without matching middle ancestor = %d, want 102", r)
	}

	mChain := func(name string) bool { return name != "System Idle Process" }
	if r := rootOf(procs, 102, mChain); r != 1 {
		t.Errorf("rootOf climbing all matching ancestors = %d, want 1", r)
	}

	mAny := func(name string) bool { return true }
	if r := rootOf(procs, 201, mAny); r != 201 {
		t.Errorf("rootOf must stop at PID-reuse boundary: got %d, want 201", r)
	}

	if r := rootOf(procs, 0, mAny); r != 0 {
		t.Errorf("rootOf self-parent PID 0 must return 0 immediately: got %d", r)
	}
}

func TestBuildRowsCyclePruningDoesNotPruneRoot(t *testing.T) {
	// A cycle among descendants (e.g. identical timestamps) must not cause
	// the root to prune itself.
	procs := map[uint32]procInfo{
		10: {ppid: 0, name: "app.exe", created: 0},
		20: {ppid: 10, name: "sub.exe", created: 0},
		30: {ppid: 20, name: "sub.exe", created: 0},
	}
	m := nameMatcher("app.exe")
	rows := buildRows(procs, matchedPIDs(procs, m), m, false, false, false)
	if len(rows) != 1 || rows[0].root != 10 {
		t.Fatalf("root was incorrectly pruned: %+v", rows)
	}
}

func TestBuildRowsSortsByCommitThenRoot(t *testing.T) {
	procs := map[uint32]procInfo{
		1:  {ppid: 0, name: "System", created: 100},
		10: {ppid: 1, name: "a.exe", created: 200, commit: 10},
		20: {ppid: 1, name: "a.exe", created: 300, commit: 99},
		30: {ppid: 1, name: "a.exe", created: 400, commit: 10},
	}
	m := nameMatcher("a.exe")
	rows := buildRows(procs, matchedPIDs(procs, m), m, false, false, true)
	want := []uint32{20, 10, 30}
	for i, w := range want {
		if rows[i].root != w {
			t.Fatalf("order = %d,%d,%d, want %v", rows[0].root, rows[1].root, rows[2].root, want)
		}
	}
}

func TestGroup(t *testing.T) {
	cases := []struct{ in, want string }{
		{"0", "0"},
		{"123", "123"},
		{"1234", "1,234"},
		{"1234567", "1,234,567"},
		{"-1234567", "-1,234,567"},
		{"1234.5", "1,234.5"},
		{"123.5", "123.5"},
	}
	for _, c := range cases {
		if got := group(c.in); got != c.want {
			t.Errorf("group(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCommas(t *testing.T) {
	if got := commasI(1234567); got != "1,234,567" {
		t.Errorf("commasI(1234567) = %q", got)
	}
	if got := commasF(mb(2 << 20)); got != "2.0" {
		t.Errorf("commasF(2 MiB) = %q, want 2.0", got)
	}
	if got := commasF(mb(1536 << 20)); got != "1,536.0" {
		t.Errorf("commasF(1536 MiB) = %q, want 1,536.0", got)
	}
	if got := mb(1 << 30); got != 1024 {
		t.Errorf("mb(1 GiB) = %v, want 1024", got)
	}
}
