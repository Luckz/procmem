// procmem - per-root-instance memory totals for a named exe.
//
// Finds every process whose name matches the input, groups each under its
// topmost matching ancestor (the "root instance") and prints combined RAM
// (private working set = Task Manager's "Memory") and commit (pagefile usage
// = Task Manager's "Commit size") of each root plus its descendant processes.
//
// Data source: a single NtQuerySystemInformation(SystemProcessInformation)
// query, which the kernel answers for every process without opening any
// handle — so svchost and other service processes are covered with no
// elevation.
//
// Matching: input ending in .exe (without *) is an exact name; /e /exact
// makes any input exact (.exe optional) and so does /ec, which additionally
// restricts descendants to the root's exact name; anything else, including
// an explicit *, matches by substring.
//
// Usage: procmem [/a | /x | /f] [/e] [/c] [/g] [/s] <name>...
//        procmem /kill [/f] [/e] [/g] [/s] <name|PID>...
//        procmem /killtree [/f] [/x] [/g] [/s] <name|PID>...
//
// Query options:
//	/a   /all      count all descendants regardless of name (default)
//	/x   /strict   eXact tree: exact root (implies /e) and exact-name children
//	/f   /flat     one row per matching process; no grouping, no rollup
//	/e   /exact    input is an exact process name, .exe optional
//	/c   /cpu      show total CPU time
//	/g   /gpu      show Dedicated and Shared GPU memory
//	/s   /short    omit the Names column
//	/h   /?  /help show this help
//
// Process termination:
//	/kill          terminate matching process(es) or <PID>
//	/killtree      terminate matching process(es) or <PID> plus all descendants
//	/f   /force    force termination; required when killing by process name
//	Options /e, /x, /g, and /s also apply (/s gives a compact list).
package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"unsafe"

	"golang.org/x/sys/windows"
)

const version = "1.1.0"

type procInfo struct {
	ppid    uint32
	name    string
	created uint64 // creation time as comparable int64 FILETIME bits
	ws      uint64 // total working set, bytes
	commit  uint64 // PagefileUsage (= Task Manager "Commit size"), bytes
	cpu     uint64 // total CPU time (UserTime + KernelTime) in 100ns units
	gpuDed  uint64 // dedicated GPU memory, bytes
	gpuShr  uint64 // shared GPU memory, bytes
}

// Input is an exact process name when forced via /e, or when it ends in
// .exe without a *; an explicit * opts a .exe-suffixed input back into
// substring matching. In fuzzy mode the * characters and a trailing .exe
// are stripped: "*team*" matches steam.exe and would match ms-teams.exe.
type matcher struct {
	exact    string // lowercase exact name (with .exe suffix)
	exactAlt string // alternative exact name without .exe (e.g. system pseudo-processes)
	pattern  string // lowercase substring; used when exact is empty
}

func (m matcher) matches(name string) bool {
	if m.exact != "" {
		return strings.EqualFold(name, m.exact) || (m.exactAlt != "" && strings.EqualFold(name, m.exactAlt))
	}
	return strings.Contains(strings.ToLower(name), m.pattern)
}

func parseName(raw string, forceExact bool) matcher {
	lower := strings.ToLower(raw)
	switch {
	case forceExact:
		if strings.HasSuffix(lower, ".exe") {
			return matcher{exact: lower}
		}
		return matcher{exact: lower + ".exe", exactAlt: lower}
	case strings.Contains(raw, "*"):
		p := strings.ReplaceAll(lower, "*", "")
		return matcher{pattern: strings.TrimSuffix(p, ".exe")}
	case strings.HasSuffix(lower, ".exe"):
		return matcher{exact: lower}
	default:
		return matcher{pattern: lower}
	}
}

func printUsage(w io.Writer) {
	fmt.Fprintf(w, "procmem %s\n\nUsage: %s [/a | /x | /f] [/e] [/c] [/g] [/s] <name>...\n", version, filepath.Base(os.Args[0]))
	fmt.Fprintf(w, "       %s /kill [/f] [/e] [/g] [/s] <name|PID>...\n", filepath.Base(os.Args[0]))
	fmt.Fprintf(w, "       %s /killtree [/f] [/x] [/g] [/s] <name|PID>...\n", filepath.Base(os.Args[0]))
	fmt.Fprint(w, "\nPer root instance of <name>: combined RAM (private working set =\n"+
		"Task Manager \"Memory\") and commit (pagefile usage = Task Manager\n"+
		"\"Commit size\") of that instance plus its descendant processes, with the\n"+
		"unique process names they use.\n"+
		"Multiple process names can be specified to query together.\n\n"+
		"Query options:\n"+
		"  /a   /all          count all descendants regardless of name (default)\n"+
		"  /x   /strict       eXact tree: exact root (implies /e) and exact-name children\n"+
		"  /f   /flat         one row per matching process; no grouping, no rollup\n"+
		"  /e   /exact        <name> is an exact process name, .exe suffix optional\n"+
		"  /c   /cpu          show total CPU time (User + Kernel)\n"+
		"  /g   /gpu          show Dedicated and Shared GPU memory\n"+
		"  /s   /short        omit the Names column\n"+
		"  /h   /?   /help    show this help\n\n"+
		"Process termination:\n"+
		"  /kill              terminate matching process(es) or <PID>\n"+
		"  /killtree          terminate matching process(es) or <PID> plus all descendants\n"+
		"  /f   /force        force termination; required when killing by process name\n"+
		"  Options /e, /x, /g, and /s also apply (/s gives a compact list).\n\n"+
		"Options can use Windows /flag, Unix -flag / --flag, and bundling (e.g. -cg, -cgs, -ef).\n"+
		"Matching: input ending in .exe is exact; anything else, or an explicit\n"+
		"* (e.g. *team*), matches by substring. Numeric input without .exe is a PID.\n")
}

func main() {
	var (
		exactInput bool
		exactKids  bool
		allKids    bool
		flat       bool
		short      bool
		showCPU    bool
		showGPU    bool
		killMode   bool
		killTree   bool
		hasF       bool
		force      bool
		positional []string
	)

	applyShort := func(ch byte) bool {
		switch ch | 0x20 {
		case 'a':
			allKids = true
		case 'x':
			exactKids = true
		case 'f':
			hasF = true
		case 'e':
			exactInput = true
		case 'c':
			showCPU = true
		case 'g':
			showGPU = true
		case 's':
			short = true
		case 'h', '?':
			printUsage(os.Stdout)
			os.Exit(0)
		default:
			return false
		}
		return true
	}

	applyNamed := func(name string) bool {
		switch strings.ToLower(name) {
		case "a", "all":
			allKids = true
		case "x", "strict", "exact-tree":
			exactKids = true
		case "f":
			hasF = true
		case "flat":
			flat = true
		case "force":
			force = true
		case "e", "exact":
			exactInput = true
		case "c", "cpu":
			showCPU = true
		case "g", "gpu":
			showGPU = true
		case "s", "short":
			short = true
		case "kill":
			killMode = true
		case "killtree", "kill-tree":
			killTree = true
		case "h", "?", "help":
			printUsage(os.Stdout)
			os.Exit(0)
		default:
			return false
		}
		return true
	}

	stopFlags := false
	for _, a := range os.Args[1:] {
		if a == "--" {
			stopFlags = true
			continue
		}
		if !stopFlags {
			if strings.HasPrefix(a, "--") && len(a) > 2 {
				if !applyNamed(a[2:]) {
					fmt.Fprintf(os.Stderr, "unknown option %s\n", a)
					printUsage(os.Stderr)
					os.Exit(2)
				}
				continue
			}
			if strings.HasPrefix(a, "/") && len(a) > 1 {
				if !applyNamed(a[1:]) {
					fmt.Fprintf(os.Stderr, "unknown option %s\n", a)
					printUsage(os.Stderr)
					os.Exit(2)
				}
				continue
			}
			if strings.HasPrefix(a, "-") && len(a) > 1 {
				if applyNamed(a[1:]) {
					continue
				}
				allValid := true
				for i := 1; i < len(a); i++ {
					if !applyShort(a[i]) {
						allValid = false
						break
					}
				}
				if allValid {
					continue
				}
				fmt.Fprintf(os.Stderr, "unknown option %s\n", a)
				printUsage(os.Stderr)
				os.Exit(2)
			}
		}
		positional = append(positional, a)
	}

	if killMode && killTree {
		fmt.Fprintln(os.Stderr, "specify at most one of /kill, /killtree")
		os.Exit(2)
	}
	if killMode || killTree {
		if flat {
			fmt.Fprintln(os.Stderr, "/flat cannot be used with /kill or /killtree")
			os.Exit(2)
		}
		if hasF || force {
			force = true
		}
		flat = false
		if allKids && exactKids {
			fmt.Fprintln(os.Stderr, "specify at most one of /a, /x")
			os.Exit(2)
		}
	} else {
		if force {
			fmt.Fprintln(os.Stderr, "/force can only be used with /kill or /killtree")
			os.Exit(2)
		}
		if hasF || flat {
			flat = true
		}
		// /a, /x and /f are three exclusive traversal scopes.
		if (allKids && exactKids) || (allKids && flat) || (exactKids && flat) {
			fmt.Fprintln(os.Stderr, "specify at most one of /a, /x, /f")
			os.Exit(2)
		}
	}

	if len(positional) < 1 {
		printUsage(os.Stderr)
		os.Exit(2)
	}
	for _, p := range positional {
		if p == "" {
			fmt.Fprintln(os.Stderr, "empty process name is not allowed")
			os.Exit(2)
		}
	}

	procs, err := snapshot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "snapshot:", err)
		os.Exit(1)
	}

	if killMode || killTree {
		runKill(procs, positional, killTree, force, exactInput, exactKids, showCPU, showGPU, short)
		return
	}

	var matchers []matcher
	for _, raw := range positional {
		matchers = append(matchers, parseName(raw, exactInput || exactKids))
	}
	matches := func(name string) bool {
		for _, m := range matchers {
			if m.matches(name) {
				return true
			}
		}
		return false
	}

	var matched []uint32
	for pid, p := range procs {
		if matches(p.name) {
			matched = append(matched, pid)
		}
	}
	targetStr := strings.Join(positional, ", ")
	if len(matched) == 0 {
		fmt.Printf("No running processes matching '%s'.\n", targetStr)
		os.Exit(1)
	}

	if showGPU {
		gpuDed, gpuShr, err := queryGPUMemory()
		if err != nil {
			fmt.Fprintf(os.Stderr, "procmem: GPU memory query failed: %v\n", err)
			os.Exit(1)
		}
		for pid, info := range procs {
			if ded, ok := gpuDed[pid]; ok {
				info.gpuDed = ded
			}
			if shr, ok := gpuShr[pid]; ok {
				info.gpuShr = shr
			}
			procs[pid] = info
		}
	}

	type row struct {
		root           uint32
		procs          int
		ws, commit     uint64
		cpu            uint64
		gpuDed, gpuShr uint64
		names          map[string]bool
	}
	var rows []row

	if flat {
		// /f: one row per matching process; no upward grouping and no
		// descendant rollup.
		for _, pid := range matched {
			var r row
			r.root = pid
			r.names = map[string]bool{strings.ToLower(procs[pid].name): true}
			r.ws, r.commit, r.procs = procs[pid].ws, procs[pid].commit, 1
			r.cpu = procs[pid].cpu
			r.gpuDed = procs[pid].gpuDed
			r.gpuShr = procs[pid].gpuShr
			rows = append(rows, r)
		}
	} else {
		kids := map[uint32][]uint32{}
		for pid, p := range procs {
			if _, ok := procs[p.ppid]; ok && p.ppid != pid {
				kids[p.ppid] = append(kids[p.ppid], pid)
			}
		}

		// Topmost ancestor whose own name also matches the input. Stops if
		// the parent is gone, does not match, or looks like PID reuse (a
		// "parent" created after the child cannot be its real parent).
		rootOf := func(pid uint32) uint32 {
			cur := pid
			for i := 0; i < 128; i++ {
				ppid := procs[cur].ppid
				parent, ok := procs[ppid]
				if !ok || !matches(parent.name) {
					return cur
				}
				if pt, ct := parent.created, procs[cur].created; pt != 0 && ct != 0 && pt > ct {
					return cur
				}
				cur = ppid
			}
			return cur
		}

		roots := map[uint32]bool{}
		for _, pid := range matched {
			roots[rootOf(pid)] = true
		}

		// Prune any root that is already traversed as a descendant of another
		// root (e.g. pattern A -> non-matching launcher -> pattern B).
		pruned := map[uint32]bool{}
		for r1 := range roots {
			if pruned[r1] {
				continue
			}
			stack := append([]uint32{}, kids[r1]...)
			seen := map[uint32]bool{}
			for len(stack) > 0 {
				pid := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				if seen[pid] {
					continue
				}
				seen[pid] = true
				if exactKids && strings.ToLower(procs[pid].name) != strings.ToLower(procs[r1].name) {
					continue
				}
				if roots[pid] {
					pruned[pid] = true
				}
				stack = append(stack, kids[pid]...)
			}
		}
		for p := range pruned {
			delete(roots, p)
		}

		for root := range roots {
			var r row
			r.names = map[string]bool{}
			rootName := strings.ToLower(procs[root].name)
			stack := []uint32{root}
			seen := map[uint32]bool{}
			for len(stack) > 0 {
				pid := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				if seen[pid] {
					continue
				}
				seen[pid] = true
				// /x /strict: count and traverse only processes named exactly like
				// their root; differently-named children are cut points.
				if exactKids && strings.ToLower(procs[pid].name) != rootName {
					continue
				}
				if !short {
					r.names[strings.ToLower(procs[pid].name)] = true
				}
				r.ws += procs[pid].ws
				r.commit += procs[pid].commit
				r.cpu += procs[pid].cpu
				r.gpuDed += procs[pid].gpuDed
				r.gpuShr += procs[pid].gpuShr
				r.procs++
				stack = append(stack, kids[pid]...)
			}
			r.root = root
			rows = append(rows, r)
		}
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].commit != rows[j].commit {
			return rows[i].commit > rows[j].commit
		}
		return rows[i].root < rows[j].root
	})

	var numBuf bytes.Buffer
	w := tabwriter.NewWriter(&numBuf, 0, 0, 2, ' ', tabwriter.AlignRight)
	if flat {
		fmt.Fprint(w, "PID\tRAM MB\tCommit MB\t")
	} else {
		fmt.Fprint(w, "RootPID\tProcs\tRAM MB\tCommit MB\t")
	}
	if showCPU {
		fmt.Fprint(w, "CPU Time\t")
	}
	if showGPU {
		fmt.Fprint(w, "GPU Ded\tGPU Shr\t")
	}
	fmt.Fprintln(w)
	for _, r := range rows {
		ram, commit := commasF(mb(r.ws)), commasF(mb(r.commit))
		if flat {
			fmt.Fprintf(w, "%d\t%s\t%s\t", r.root, ram, commit)
		} else {
			fmt.Fprintf(w, "%d\t%s\t%s\t%s\t", r.root, commasI(r.procs), ram, commit)
		}
		if showCPU {
			fmt.Fprintf(w, "%s\t", formatCPU(r.cpu))
		}
		if showGPU {
			fmt.Fprintf(w, "%s\t%s\t", commasF(mb(r.gpuDed)), commasF(mb(r.gpuShr)))
		}
		fmt.Fprintln(w)
	}
	w.Flush()

	lines := strings.Split(strings.TrimRight(numBuf.String(), "\n"), "\n")
	if short {
		for _, line := range lines {
			fmt.Println(line)
		}
	} else {
		fmt.Printf("%s  Names\n", lines[0])
		for i, r := range rows {
			rootName := strings.ToLower(procs[r.root].name)
			var otherNames []string
			for n := range r.names {
				if n != rootName {
					otherNames = append(otherNames, n)
				}
			}
			sort.Strings(otherNames)
			names := make([]string, 0, len(r.names))
			if rootName != "" {
				names = append(names, rootName)
			}
			names = append(names, otherNames...)
			for j, n := range names {
				if strings.Contains(n, " ") {
					names[j] = `"` + n + `"`
				}
			}
			fmt.Printf("%s  %s\n", lines[i+1], strings.Join(names, " "))
		}
	}

	var totProcs int
	var totWS, totCommit, totCPU, totGPUDed, totGPUShr uint64
	for _, r := range rows {
		totProcs += r.procs
		totWS += r.ws
		totCommit += r.commit
		totCPU += r.cpu
		totGPUDed += r.gpuDed
		totGPUShr += r.gpuShr
	}
	cpuSummary := ""
	if showCPU {
		cpuSummary = fmt.Sprintf(", CPU %s", formatCPU(totCPU))
	}
	gpuSummary := ""
	if showGPU {
		gpuSummary = fmt.Sprintf(", GPU ded %s MB, shared %s MB", commasF(mb(totGPUDed)), commasF(mb(totGPUShr)))
	}
	if flat {
		fmt.Printf("=> %s process(es) matching '%s': RAM %s MB, commit %s MB%s%s\n",
			commasI(totProcs), targetStr, commasF(mb(totWS)), commasF(mb(totCommit)), cpuSummary, gpuSummary)
	} else {
		fmt.Printf("=> %d root(s), %s process(es) matching '%s': RAM %s MB, commit %s MB%s%s\n",
			len(rows), commasI(totProcs), targetStr, commasF(mb(totWS)), commasF(mb(totCommit)), cpuSummary, gpuSummary)
	}
}

func runKill(
	procs map[uint32]procInfo,
	positional []string,
	killTree bool,
	force bool,
	exactInput bool,
	exactKids bool,
	showCPU bool,
	showGPU bool,
	short bool,
) {
	// CPU time is always included on the kill and killtree use path.
	showCPU = true

	type targetItem struct {
		raw     string
		isPID   bool
		pid     uint32
		matcher matcher
	}
	var targets []targetItem
	hasNameTarget := false
	for _, raw := range positional {
		if !strings.Contains(raw, "*") && !strings.HasSuffix(strings.ToLower(raw), ".exe") && !exactInput {
			if pidVal, err := strconv.ParseUint(raw, 10, 32); err == nil {
				targets = append(targets, targetItem{raw: raw, isPID: true, pid: uint32(pidVal)})
				continue
			}
		}
		m := parseName(raw, exactInput || exactKids)
		if m.exact == "" && m.pattern == "" {
			fmt.Fprintf(os.Stderr, "wildcard-only pattern '%s' would match every process; not allowed in kill mode\n", raw)
			os.Exit(2)
		}
		hasNameTarget = true
		targets = append(targets, targetItem{raw: raw, isPID: false, matcher: m})
	}

	kids := map[uint32][]uint32{}
	for pid, p := range procs {
		if _, ok := procs[p.ppid]; ok && p.ppid != pid {
			kids[p.ppid] = append(kids[p.ppid], pid)
		}
	}

	if showGPU {
		gpuDed, gpuShr, err := queryGPUMemory()
		if err != nil {
			fmt.Fprintf(os.Stderr, "procmem: GPU memory query failed: %v\n", err)
			os.Exit(1)
		}
		for pid, info := range procs {
			if ded, ok := gpuDed[pid]; ok {
				info.gpuDed = ded
			}
			if shr, ok := gpuShr[pid]; ok {
				info.gpuShr = shr
			}
			procs[pid] = info
		}
	}

	// Safety check: killing by process name strictly requires /f or /force
	if hasNameTarget && !force {
		var matched []uint32
		matchedSet := make(map[uint32]bool)
		for _, t := range targets {
			if t.isPID {
				if _, ok := procs[t.pid]; ok && !matchedSet[t.pid] {
					matchedSet[t.pid] = true
					matched = append(matched, t.pid)
				}
			}
		}
		for pid, p := range procs {
			for _, t := range targets {
				if !t.isPID && t.matcher.matches(p.name) {
					if !matchedSet[pid] {
						matchedSet[pid] = true
						matched = append(matched, pid)
					}
					break
				}
			}
		}
		if len(matched) == 0 {
			targetStr := strings.Join(positional, ", ")
			fmt.Fprintf(os.Stderr, "No running processes matching '%s'.\n", targetStr)
			os.Exit(1)
		}
		killCmd := "/kill"
		if killTree {
			killCmd = "/killtree"
		}
		targetStr := strings.Join(positional, " ")

		if short {
			// Compact de-verbosed view
			fmt.Fprintf(os.Stderr, "procmem: killing by process name requires /f or /force (e.g. procmem %s /f %s)\n", killCmd, targetStr)
			fmt.Fprintf(os.Stderr, "Matched %d process(es):\n", len(matched))
			sort.Slice(matched, func(i, j int) bool { return matched[i] < matched[j] })
			for _, pid := range matched {
				fmt.Fprintf(os.Stderr, "  PID %-6d %s\n", pid, procs[pid].name)
			}
			fmt.Fprintln(os.Stderr, "Use /e /f (or --kill -ef) to restrict to exact process names.")
			os.Exit(2)
		}

		if !killTree {
			// Flat preview
			type flatRow struct {
				pid     uint32
				name    string
				ws      uint64
				commit  uint64
				cpu     uint64
				gpuDed  uint64
				gpuShr  uint64
			}
			var rows []flatRow
			var totWS, totCommit, totCPU, totGPUDed, totGPUShr uint64
			maxPIDLen := len("PID")
			maxNameLen := len("Name")
			maxRAMLen := len("RAM MB")
			maxCommitLen := len("Commit MB")
			maxCPULen := len("CPU Time")
			maxGPUDedLen := len("GPU Ded")
			maxGPUShrLen := len("GPU Shr")

			for _, pid := range matched {
				p := procs[pid]
				totWS += p.ws
				totCommit += p.commit
				totCPU += p.cpu
				totGPUDed += p.gpuDed
				totGPUShr += p.gpuShr
				rows = append(rows, flatRow{
					pid:    pid,
					name:   p.name,
					ws:     p.ws,
					commit: p.commit,
					cpu:    p.cpu,
					gpuDed: p.gpuDed,
					gpuShr: p.gpuShr,
				})
			}
			sort.Slice(rows, func(i, j int) bool {
				if rows[i].commit != rows[j].commit {
					return rows[i].commit > rows[j].commit
				}
				return rows[i].pid < rows[j].pid
			})
			for _, r := range rows {
				pidStr := strconv.Itoa(int(r.pid))
				if len(pidStr) > maxPIDLen {
					maxPIDLen = len(pidStr)
				}
				if len(r.name) > maxNameLen {
					maxNameLen = len(r.name)
				}
				ramStr := commasF(mb(r.ws))
				if len(ramStr) > maxRAMLen {
					maxRAMLen = len(ramStr)
				}
				commitStr := commasF(mb(r.commit))
				if len(commitStr) > maxCommitLen {
					maxCommitLen = len(commitStr)
				}
				cpuStr := formatCPU(r.cpu)
				if len(cpuStr) > maxCPULen {
					maxCPULen = len(cpuStr)
				}
				if showGPU {
					dedStr := commasF(mb(r.gpuDed))
					if len(dedStr) > maxGPUDedLen {
						maxGPUDedLen = len(dedStr)
					}
					shrStr := commasF(mb(r.gpuShr))
					if len(shrStr) > maxGPUShrLen {
						maxGPUShrLen = len(shrStr)
					}
				}
			}

			fmt.Fprintf(os.Stderr, "procmem: killing by process name requires /f or /force (e.g. procmem %s /f %s)\n\n", killCmd, targetStr)

			var header strings.Builder
			fmt.Fprintf(&header, "%*s  %-*s  %*s  %*s  %*s",
				maxPIDLen, "PID", maxNameLen, "Name", maxRAMLen, "RAM MB", maxCommitLen, "Commit MB", maxCPULen, "CPU Time")
			if showGPU {
				fmt.Fprintf(&header, "  %*s  %*s", maxGPUDedLen, "GPU Ded", maxGPUShrLen, "GPU Shr")
			}
			fmt.Fprintln(os.Stderr, header.String())

			for _, r := range rows {
				var row strings.Builder
				fmt.Fprintf(&row, "%*d  %-*s  %*s  %*s  %*s",
					maxPIDLen, r.pid, maxNameLen, r.name, maxRAMLen, commasF(mb(r.ws)), maxCommitLen, commasF(mb(r.commit)), maxCPULen, formatCPU(r.cpu))
				if showGPU {
					fmt.Fprintf(&row, "  %*s  %*s", maxGPUDedLen, commasF(mb(r.gpuDed)), maxGPUShrLen, commasF(mb(r.gpuShr)))
				}
				fmt.Fprintln(os.Stderr, row.String())
			}

			gpuSummary := ""
			if showGPU {
				gpuSummary = fmt.Sprintf(", GPU ded %s MB, shared %s MB", commasF(mb(totGPUDed)), commasF(mb(totGPUShr)))
			}
			targetJoined := strings.Join(positional, ", ")
			fmt.Fprintf(os.Stderr, "=> %s process(es) matching '%s': RAM %s MB, commit %s MB, CPU %s%s\n\n",
				commasI(len(rows)), targetJoined, commasF(mb(totWS)), commasF(mb(totCommit)), formatCPU(totCPU), gpuSummary)

			fmt.Fprintf(os.Stderr, "Use /f to confirm: procmem %s /f %s\n", killCmd, targetStr)
			if len(rows) > 0 {
				fmt.Fprintf(os.Stderr, "Or target a single PID: procmem %s %d\n", killCmd, rows[0].pid)
			}
			fmt.Fprintln(os.Stderr, "Use /e /f (or --kill -ef) to restrict to exact process names.")
			os.Exit(2)
		} else {
			// Tree preview
			rootOf := func(pid uint32, m matcher) uint32 {
				cur := pid
				for i := 0; i < 128; i++ {
					ppid := procs[cur].ppid
					parent, ok := procs[ppid]
					if !ok || !m.matches(parent.name) {
						return cur
					}
					if pt, ct := parent.created, procs[cur].created; pt != 0 && ct != 0 && pt > ct {
						return cur
					}
					cur = ppid
				}
				return cur
			}

			var roots []uint32
			seenRoots := make(map[uint32]bool)
			for _, t := range targets {
				if t.isPID {
					if _, ok := procs[t.pid]; ok && !seenRoots[t.pid] {
						seenRoots[t.pid] = true
						roots = append(roots, t.pid)
					}
				}
			}
			for pid, p := range procs {
				for _, t := range targets {
					if !t.isPID && t.matcher.matches(p.name) {
						r := rootOf(pid, t.matcher)
						if !seenRoots[r] {
							seenRoots[r] = true
							roots = append(roots, r)
						}
						break
					}
				}
			}

			type treeRow struct {
				rootPID    uint32
				procsCount int
				rootName   string
				otherNames []string
				ws         uint64
				commit     uint64
				cpu        uint64
				gpuDed     uint64
				gpuShr     uint64
			}
			var treeRows []treeRow
			var totWS, totCommit, totCPU, totGPUDed, totGPUShr uint64
			var totProcs int

			for _, r := range roots {
				rootName := procs[r].name
				namesSet := make(map[string]bool)
				queue := []uint32{r}
				var treeWS, treeCommit, treeCPU, treeGPUDed, treeGPUShr uint64
				seenTree := make(map[uint32]bool)
				for i := 0; i < len(queue); i++ {
					curr := queue[i]
					seenTree[curr] = true
					p := procs[curr]
					treeWS += p.ws
					treeCommit += p.commit
					treeCPU += p.cpu
					treeGPUDed += p.gpuDed
					treeGPUShr += p.gpuShr
					if !strings.EqualFold(p.name, rootName) {
						namesSet[p.name] = true
					}
					currName := strings.ToLower(p.name)
					for _, child := range kids[curr] {
						if seenTree[child] {
							continue
						}
						if pt, ct := procs[curr].created, procs[child].created; pt != 0 && ct != 0 && pt > ct {
							continue
						}
						if exactKids && strings.ToLower(procs[child].name) != currName {
							continue
						}
						queue = append(queue, child)
					}
				}

				var otherNames []string
				for n := range namesSet {
					otherNames = append(otherNames, n)
				}
				sort.Strings(otherNames)

				totWS += treeWS
				totCommit += treeCommit
				totCPU += treeCPU
				totGPUDed += treeGPUDed
				totGPUShr += treeGPUShr
				totProcs += len(queue)

				treeRows = append(treeRows, treeRow{
					rootPID:    r,
					procsCount: len(queue),
					rootName:   rootName,
					otherNames: otherNames,
					ws:         treeWS,
					commit:     treeCommit,
					cpu:        treeCPU,
					gpuDed:     treeGPUDed,
					gpuShr:     treeGPUShr,
				})
			}

			sort.Slice(treeRows, func(i, j int) bool {
				if treeRows[i].commit != treeRows[j].commit {
					return treeRows[i].commit > treeRows[j].commit
				}
				return treeRows[i].rootPID < treeRows[j].rootPID
			})

			maxPIDLen := len("RootPID")
			maxProcsLen := len("Procs")
			maxNameLen := len("Name")
			maxRAMLen := len("RAM MB")
			maxCommitLen := len("Commit MB")
			maxCPULen := len("CPU Time")
			maxGPUDedLen := len("GPU Ded")
			maxGPUShrLen := len("GPU Shr")

			for _, r := range treeRows {
				pidStr := strconv.Itoa(int(r.rootPID))
				if len(pidStr) > maxPIDLen {
					maxPIDLen = len(pidStr)
				}
				procsStr := strconv.Itoa(r.procsCount)
				if len(procsStr) > maxProcsLen {
					maxProcsLen = len(procsStr)
				}
				if len(r.rootName) > maxNameLen {
					maxNameLen = len(r.rootName)
				}
				for _, n := range r.otherNames {
					if len(n) > maxNameLen {
						maxNameLen = len(n)
					}
				}
				ramStr := commasF(mb(r.ws))
				if len(ramStr) > maxRAMLen {
					maxRAMLen = len(ramStr)
				}
				commitStr := commasF(mb(r.commit))
				if len(commitStr) > maxCommitLen {
					maxCommitLen = len(commitStr)
				}
				cpuStr := formatCPU(r.cpu)
				if len(cpuStr) > maxCPULen {
					maxCPULen = len(cpuStr)
				}
				if showGPU {
					dedStr := commasF(mb(r.gpuDed))
					if len(dedStr) > maxGPUDedLen {
						maxGPUDedLen = len(dedStr)
					}
					shrStr := commasF(mb(r.gpuShr))
					if len(shrStr) > maxGPUShrLen {
						maxGPUShrLen = len(shrStr)
					}
				}
			}

			fmt.Fprintf(os.Stderr, "procmem: killing by process name requires /f or /force (e.g. procmem %s /f %s)\n\n", killCmd, targetStr)

			var header strings.Builder
			fmt.Fprintf(&header, "%*s  %*s  %-*s  %*s  %*s  %*s",
				maxPIDLen, "RootPID", maxProcsLen, "Procs", maxNameLen, "Name", maxRAMLen, "RAM MB", maxCommitLen, "Commit MB", maxCPULen, "CPU Time")
			if showGPU {
				fmt.Fprintf(&header, "  %*s  %*s", maxGPUDedLen, "GPU Ded", maxGPUShrLen, "GPU Shr")
			}
			fmt.Fprintln(os.Stderr, header.String())

			for _, r := range treeRows {
				var row strings.Builder
				fmt.Fprintf(&row, "%*d  %*d  %-*s  %*s  %*s  %*s",
					maxPIDLen, r.rootPID, maxProcsLen, r.procsCount, maxNameLen, r.rootName, maxRAMLen, commasF(mb(r.ws)), maxCommitLen, commasF(mb(r.commit)), maxCPULen, formatCPU(r.cpu))
				if showGPU {
					fmt.Fprintf(&row, "  %*s  %*s", maxGPUDedLen, commasF(mb(r.gpuDed)), maxGPUShrLen, commasF(mb(r.gpuShr)))
				}
				fmt.Fprintln(os.Stderr, row.String())
				for _, childName := range r.otherNames {
					var childRow strings.Builder
					fmt.Fprintf(&childRow, "%*s  %*s  %-*s",
						maxPIDLen, "", maxProcsLen, "", maxNameLen, childName)
					fmt.Fprintln(os.Stderr, childRow.String())
				}
			}

			gpuSummary := ""
			if showGPU {
				gpuSummary = fmt.Sprintf(", GPU ded %s MB, shared %s MB", commasF(mb(totGPUDed)), commasF(mb(totGPUShr)))
			}
			targetJoined := strings.Join(positional, ", ")
			fmt.Fprintf(os.Stderr, "=> %d root(s), %s process(es) matching '%s': RAM %s MB, commit %s MB, CPU %s%s\n\n",
				len(treeRows), commasI(totProcs), targetJoined, commasF(mb(totWS)), commasF(mb(totCommit)), formatCPU(totCPU), gpuSummary)

			fmt.Fprintf(os.Stderr, "Use /f to confirm: procmem %s /f %s\n", killCmd, targetStr)
			if len(treeRows) > 0 {
				fmt.Fprintf(os.Stderr, "Or target a single PID: procmem %s %d\n", killCmd, treeRows[0].rootPID)
			}
			fmt.Fprintln(os.Stderr, "Use /e /f (or --kill -ef) to restrict to exact process names.")
			os.Exit(2)
		}
	}

	var toKill []uint32
	seenKill := make(map[uint32]bool)
	addToKill := func(pid uint32) {
		if !seenKill[pid] {
			seenKill[pid] = true
			toKill = append(toKill, pid)
		}
	}
	targetErrors := false

	if !killTree {
		for _, t := range targets {
			if t.isPID {
				if _, ok := procs[t.pid]; ok {
					addToKill(t.pid)
				} else {
					fmt.Fprintf(os.Stderr, "No running process with PID %d.\n", t.pid)
					targetErrors = true
				}
			} else {
				matchedThis := 0
				for pid, p := range procs {
					if t.matcher.matches(p.name) {
						addToKill(pid)
						matchedThis++
					}
				}
				if matchedThis == 0 {
					fmt.Fprintf(os.Stderr, "No running processes matching '%s'.\n", t.raw)
					targetErrors = true
				}
			}
		}
	} else {
		rootOf := func(pid uint32, m matcher) uint32 {
			cur := pid
			for i := 0; i < 128; i++ {
				ppid := procs[cur].ppid
				parent, ok := procs[ppid]
				if !ok || !m.matches(parent.name) {
					return cur
				}
				if pt, ct := parent.created, procs[cur].created; pt != 0 && ct != 0 && pt > ct {
					return cur
				}
				cur = ppid
			}
			return cur
		}

		collectTree := func(root uint32) {
			queue := []uint32{root}
			for i := 0; i < len(queue); i++ {
				curr := queue[i]
				addToKill(curr)
				currName := strings.ToLower(procs[curr].name)
				for _, child := range kids[curr] {
					if seenKill[child] {
						continue
					}
					if pt, ct := procs[curr].created, procs[child].created; pt != 0 && ct != 0 && pt > ct {
						continue
					}
					if exactKids && strings.ToLower(procs[child].name) != currName {
						continue
					}
					queue = append(queue, child)
				}
			}
		}

		for _, t := range targets {
			if t.isPID {
				if _, ok := procs[t.pid]; ok {
					collectTree(t.pid)
				} else {
					fmt.Fprintf(os.Stderr, "No running process with PID %d.\n", t.pid)
					targetErrors = true
				}
			} else {
				var roots []uint32
				seenRoots := make(map[uint32]bool)
				for pid, p := range procs {
					if t.matcher.matches(p.name) {
						r := rootOf(pid, t.matcher)
						if !seenRoots[r] {
							seenRoots[r] = true
							roots = append(roots, r)
						}
					}
				}
				if len(roots) == 0 {
					fmt.Fprintf(os.Stderr, "No running processes matching '%s'.\n", t.raw)
					targetErrors = true
				} else {
					for _, r := range roots {
						collectTree(r)
					}
				}
			}
		}
	}

	if len(toKill) == 0 {
		os.Exit(1)
	}

	type killResult struct {
		pid    uint32
		name   string
		ws     uint64
		commit uint64
		cpu    uint64
		gpuDed uint64
		gpuShr uint64
		err    error
	}

	var results []killResult
	var freedWS, freedCommit, freedCPU, freedGPUDed, freedGPUShr uint64
	var successCount, failCount int

	for _, pid := range toKill {
		p := procs[pid]
		name := p.name
		if name == "" {
			if pid == 0 {
				name = "System Idle Process"
			} else {
				name = "Unknown"
			}
		}
		res := killResult{
			pid:    pid,
			name:   name,
			ws:     p.ws,
			commit: p.commit,
			cpu:    p.cpu,
			gpuDed: p.gpuDed,
			gpuShr: p.gpuShr,
		}
		if pid == 0 {
			res.err = fmt.Errorf("cannot terminate System Idle Process")
		} else {
			h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, pid)
			if err != nil {
				res.err = err
			} else {
				if err := windows.TerminateProcess(h, 1); err != nil {
					res.err = err
				}
				windows.CloseHandle(h)
			}
		}

		if res.err == nil {
			successCount++
			freedWS += res.ws
			freedCommit += res.commit
			freedCPU += res.cpu
			freedGPUDed += res.gpuDed
			freedGPUShr += res.gpuShr
		} else {
			failCount++
		}
		results = append(results, res)
	}

	if !short {
		for _, res := range results {
			if res.err == nil {
				fmt.Printf("Killed PID %-6d %s\n", res.pid, res.name)
			} else {
				fmt.Fprintf(os.Stderr, "Failed to kill PID %-6d %s: %v\n", res.pid, res.name, res.err)
			}
		}
	}

	cpuSummary := fmt.Sprintf(", CPU %s", formatCPU(freedCPU))
	gpuSummary := ""
	if showGPU {
		gpuSummary = fmt.Sprintf(", GPU ded %s MB, shared %s MB", commasF(mb(freedGPUDed)), commasF(mb(freedGPUShr)))
	}
	targetStr := strings.Join(positional, ", ")
	if failCount == 0 {
		if len(results) == 1 {
			fmt.Printf("=> Killed PID %d (%s): freed %s MB RAM, %s MB commit%s%s\n",
				results[0].pid, results[0].name, commasF(mb(freedWS)), commasF(mb(freedCommit)), cpuSummary, gpuSummary)
		} else {
			fmt.Printf("=> Killed %s process(es) matching '%s': freed %s MB RAM, %s MB commit%s%s\n",
				commasI(successCount), targetStr, commasF(mb(freedWS)), commasF(mb(freedCommit)), cpuSummary, gpuSummary)
		}
	} else {
		fmt.Printf("=> Killed %s of %s process(es) matching '%s': freed %s MB RAM, %s MB commit%s%s\n",
			commasI(successCount), commasI(successCount+failCount), targetStr, commasF(mb(freedWS)), commasF(mb(freedCommit)), cpuSummary, gpuSummary)
	}

	if failCount > 0 || targetErrors {
		os.Exit(1)
	}
	os.Exit(0)
}

func mb(v uint64) float64 { return float64(v) / (1 << 20) }

// group inserts thousands separators into the integer part of a plain
// decimal number string ("5970.0" -> "5,970.0").
func group(s string) string {
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	intPart, frac := s, ""
	if dot := strings.IndexByte(s, '.'); dot >= 0 {
		intPart, frac = s[:dot], s[dot:]
	}
	if len(intPart) > 3 {
		var b strings.Builder
		for i := 0; i < len(intPart); i++ {
			if i > 0 && (len(intPart)-i)%3 == 0 {
				b.WriteByte(',')
			}
			b.WriteByte(intPart[i])
		}
		intPart = b.String()
	}
	if neg {
		intPart = "-" + intPart
	}
	return intPart + frac
}

func commasF(v float64) string { return group(strconv.FormatFloat(v, 'f', 1, 64)) }

func commasI(v int) string { return group(strconv.Itoa(v)) }

// NtQuerySystemInformation lives in ntdll; x/sys/windows ships the
// SYSTEM_PROCESS_INFORMATION struct but no call wrapper.
var procNtQuerySystemInformation = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtQuerySystemInformation")

const (
	systemProcessInformation = 5
	statusInfoLengthMismatch = 0xC0000004
)

// snapshot returns every process's name, parent PID, creation time and
// memory usage from one kernel-side query. No process handle is opened, so
// the DACLs that deny OpenProcess on service processes (svchost, WmiPrvSE,
// ...) are never consulted — every account is readable without elevation.
func snapshot() (map[uint32]procInfo, error) {
	size := uint32(2 << 20)
	var buf []byte
	for {
		buf = make([]byte, size)
		var retLen uint32
		st, _, _ := procNtQuerySystemInformation.Call(
			systemProcessInformation,
			uintptr(unsafe.Pointer(&buf[0])),
			uintptr(size),
			uintptr(unsafe.Pointer(&retLen)),
		)
		if st == 0 {
			break
		}
		if st != statusInfoLengthMismatch {
			return nil, fmt.Errorf("NtQuerySystemInformation: NTSTATUS 0x%x", st)
		}
		if retLen > size {
			size = retLen + (256 << 10)
		} else {
			size *= 2
		}
	}
	procs := make(map[uint32]procInfo)
	for off := uintptr(0); ; {
		spi := (*windows.SYSTEM_PROCESS_INFORMATION)(unsafe.Pointer(&buf[off]))
		var name string
		// ImageName is empty for the System Idle Process (PID 0).
		if spi.ImageName.Length > 0 && spi.ImageName.Buffer != nil {
			name = windows.UTF16ToString(unsafe.Slice(spi.ImageName.Buffer, int(spi.ImageName.Length)/2))
		}
		var ws uint64
		if spi.WorkingSetPrivateSize > 0 {
			ws = uint64(spi.WorkingSetPrivateSize)
		}
		var cpu uint64
		if spi.UserTime > 0 {
			cpu += uint64(spi.UserTime)
		}
		if spi.KernelTime > 0 {
			cpu += uint64(spi.KernelTime)
		}
		procs[uint32(spi.UniqueProcessID)] = procInfo{
			ppid:    uint32(spi.InheritedFromUniqueProcessID),
			name:    name,
			created: uint64(spi.CreateTime),
			ws:      ws,
			commit:  uint64(spi.PagefileUsage),
			cpu:     cpu,
		}
		if spi.NextEntryOffset == 0 {
			return procs, nil
		}
		off += uintptr(spi.NextEntryOffset)
	}
}

// formatCPU formats a duration in 100ns ticks into [D:]HH:MM:SS, where hours
// are always zero-padded, and days (D:) are only included when >= 24 hours.
func formatCPU(ticks uint64) string {
	sec := ticks / 10000000
	s := sec % 60
	m := (sec / 60) % 60
	h := (sec / 3600) % 24
	d := sec / 86400
	if d > 0 {
		return fmt.Sprintf("%d:%02d:%02d:%02d", d, h, m, s)
	}
	return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}

var (
	modpdh                           = windows.NewLazySystemDLL("pdh.dll")
	procPdhOpenQueryW                = modpdh.NewProc("PdhOpenQueryW")
	procPdhAddEnglishCounterW        = modpdh.NewProc("PdhAddEnglishCounterW")
	procPdhCollectQueryData          = modpdh.NewProc("PdhCollectQueryData")
	procPdhGetFormattedCounterArrayW = modpdh.NewProc("PdhGetFormattedCounterArrayW")
	procPdhCloseQuery                = modpdh.NewProc("PdhCloseQuery")
)

const (
	pdhFmtLarge = 0x00000400
	pdhMoreData = 0x800007D2
)

type pdhCounterValueItem struct {
	szName   *uint16
	cStatus  uint32
	largeVal int64
}

// queryGPUMemory queries dedicated and shared GPU memory per process (in bytes)
// via PDH Performance Counters (\GPU Process Memory(*)\Dedicated Usage and Shared Usage).
func queryGPUMemory() (dedicated, shared map[uint32]uint64, err error) {
	if err := modpdh.Load(); err != nil {
		return nil, nil, fmt.Errorf("pdh.dll unavailable: %w", err)
	}

	var hQuery uintptr
	if r1, _, _ := procPdhOpenQueryW.Call(0, 0, uintptr(unsafe.Pointer(&hQuery))); r1 != 0 {
		return nil, nil, fmt.Errorf("PdhOpenQuery error 0x%08x", uint32(r1))
	}
	defer procPdhCloseQuery.Call(hQuery)

	var hDedicated, hShared uintptr
	pDedicated, _ := windows.UTF16PtrFromString(`\GPU Process Memory(*)\Dedicated Usage`)
	pShared, _ := windows.UTF16PtrFromString(`\GPU Process Memory(*)\Shared Usage`)

	if r1, _, _ := procPdhAddEnglishCounterW.Call(hQuery, uintptr(unsafe.Pointer(pDedicated)), 0, uintptr(unsafe.Pointer(&hDedicated))); r1 != 0 {
		return nil, nil, fmt.Errorf("failed to add GPU Dedicated counter: 0x%08x", uint32(r1))
	}
	if r1, _, _ := procPdhAddEnglishCounterW.Call(hQuery, uintptr(unsafe.Pointer(pShared)), 0, uintptr(unsafe.Pointer(&hShared))); r1 != 0 {
		return nil, nil, fmt.Errorf("failed to add GPU Shared counter: 0x%08x", uint32(r1))
	}

	if r1, _, _ := procPdhCollectQueryData.Call(hQuery); r1 != 0 {
		return nil, nil, fmt.Errorf("PdhCollectQueryData error 0x%08x", uint32(r1))
	}

	readCounter := func(hCounter uintptr) (map[uint32]uint64, error) {
		var bufSize, itemCount uint32
		r1, _, _ := procPdhGetFormattedCounterArrayW.Call(
			hCounter,
			pdhFmtLarge,
			uintptr(unsafe.Pointer(&bufSize)),
			uintptr(unsafe.Pointer(&itemCount)),
			0,
		)
		if r1 != 0 && uint32(r1) != pdhMoreData {
			return nil, fmt.Errorf("PdhGetFormattedCounterArray size error 0x%08x", uint32(r1))
		}
		if bufSize == 0 || itemCount == 0 {
			return make(map[uint32]uint64), nil
		}

		buf := make([]byte, bufSize)
		r1, _, _ = procPdhGetFormattedCounterArrayW.Call(
			hCounter,
			pdhFmtLarge,
			uintptr(unsafe.Pointer(&bufSize)),
			uintptr(unsafe.Pointer(&itemCount)),
			uintptr(unsafe.Pointer(&buf[0])),
		)
		if r1 != 0 {
			return nil, fmt.Errorf("PdhGetFormattedCounterArray error 0x%08x", uint32(r1))
		}

		res := make(map[uint32]uint64)
		itemSize := unsafe.Sizeof(pdhCounterValueItem{})
		for i := uint32(0); i < itemCount; i++ {
			item := (*pdhCounterValueItem)(unsafe.Pointer(&buf[uintptr(i)*itemSize]))
			name := windows.UTF16PtrToString(item.szName)
			if strings.HasPrefix(name, "pid_") {
				rest := name[4:]
				idx := strings.IndexByte(rest, '_')
				if idx > 0 {
					if pid64, err := strconv.ParseUint(rest[:idx], 10, 32); err == nil {
						if item.cStatus == 0 && item.largeVal > 0 {
							res[uint32(pid64)] += uint64(item.largeVal)
						}
					}
				}
			}
		}
		return res, nil
	}

	ded, err := readCounter(hDedicated)
	if err != nil {
		return nil, nil, err
	}
	shr, err := readCounter(hShared)
	if err != nil {
		return nil, nil, err
	}
	return ded, shr, nil
}

