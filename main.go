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
// Usage: procmem [/a | /ec | /f] [/e] [/s] <name>...
//
//	/a  /all       count all descendants regardless of name (default)
//	/ec            exact root name (implies /e) and exact-name children
//	/f  /flat      one row per matching process; no grouping, no rollup
//	/e  /exact     input is an exact process name, .exe optional
//	/s  /short     omit the Names column
//	/h  /?  /help  show this help
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

const version = "1.0.0"

type procInfo struct {
	ppid    uint32
	name    string
	created uint64 // creation time as comparable int64 FILETIME bits
	ws      uint64 // total working set, bytes
	commit  uint64 // PagefileUsage (= Task Manager "Commit size"), bytes
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
	fmt.Fprintf(w, "procmem %s\n\nUsage: %s [/a | /ec | /f] [/e] [/s] <name>...\n", version, filepath.Base(os.Args[0]))
	fmt.Fprint(w, "\nPer root instance of <name>: combined RAM (private working set =\n"+
		"Task Manager \"Memory\") and commit (pagefile usage = Task Manager\n"+
		"\"Commit size\") of that instance plus its descendant processes, with the\n"+
		"unique process names they use.\n"+
		"Multiple process names can be specified to query together.\n\n"+
		"  /a  /all       count all descendants regardless of name (default)\n"+
		"  /ec            exact root name (implies /e); count only descendants\n"+
		"                 named exactly like the root\n"+
		"  /f  /flat      one row per matching process; no grouping, no rollup\n"+
		"  /e  /exact     <name> is an exact process name, .exe suffix optional\n"+
		"  /s  /short     omit the Names column\n"+
		"  /h  /?  /help  show this help\n\n"+
		"Matching: input ending in .exe is exact; anything else, or an explicit\n"+
		"* (e.g. *team*), matches by substring.\n")
}

func main() {
	var (
		exactInput bool
		exactKids  bool
		allKids    bool
		flat       bool
		short      bool
		positional []string
	)
	for _, a := range os.Args[1:] {
		switch strings.ToLower(a) {
		case "/a", "/all":
			allKids = true
		case "/ec":
			exactKids = true
		case "/f", "/flat":
			flat = true
		case "/e", "/exact":
			exactInput = true
		case "/s", "/short":
			short = true
		case "/h", "/?", "/help":
			printUsage(os.Stdout)
			return
		default:
			if len(a) > 1 && strings.HasPrefix(a, "/") {
				fmt.Fprintf(os.Stderr, "unknown option %s\n", a)
				printUsage(os.Stderr)
				os.Exit(2)
			}
			positional = append(positional, a)
		}
	}
	// /a, /ec and /f are three exclusive traversal scopes.
	if (allKids && exactKids) || (allKids && flat) || (exactKids && flat) {
		fmt.Fprintln(os.Stderr, "specify at most one of /a, /ec, /f")
		os.Exit(2)
	}
	if len(positional) < 1 {
		printUsage(os.Stderr)
		os.Exit(2)
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

	procs, err := snapshot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "snapshot:", err)
		os.Exit(1)
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

	type row struct {
		root       uint32
		procs      int
		ws, commit uint64
		names      map[string]bool
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
				// /ec: count and traverse only processes named exactly like
				// their root; differently-named children are cut points.
				if exactKids && strings.ToLower(procs[pid].name) != rootName {
					continue
				}
				if !short {
					r.names[strings.ToLower(procs[pid].name)] = true
				}
				r.ws += procs[pid].ws
				r.commit += procs[pid].commit
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
		fmt.Fprintln(w, "PID\tRAM MB\tCommit MB\t")
	} else {
		fmt.Fprintln(w, "RootPID\tProcs\tRAM MB\tCommit MB\t")
	}
	for _, r := range rows {
		ram, commit := commasF(mb(r.ws)), commasF(mb(r.commit))
		if flat {
			fmt.Fprintf(w, "%d\t%s\t%s\t\n", r.root, ram, commit)
		} else {
			fmt.Fprintf(w, "%d\t%s\t%s\t%s\t\n", r.root, commasI(r.procs), ram, commit)
		}
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
			names := make([]string, 0, len(r.names))
			for n := range r.names {
				names = append(names, n)
			}
			sort.Strings(names)
			for j, n := range names {
				if strings.Contains(n, " ") {
					names[j] = `"` + n + `"`
				}
			}
			fmt.Printf("%s  %s\n", lines[i+1], strings.Join(names, " "))
		}
	}

	var totProcs int
	var totWS, totCommit uint64
	for _, r := range rows {
		totProcs += r.procs
		totWS += r.ws
		totCommit += r.commit
	}
	if flat {
		fmt.Printf("=> %s process(es) matching '%s': RAM %s MB, commit %s MB\n",
			commasI(totProcs), targetStr, commasF(mb(totWS)), commasF(mb(totCommit)))
	} else {
		fmt.Printf("=> %d root(s), %s process(es) matching '%s': RAM %s MB, commit %s MB\n",
			len(rows), commasI(totProcs), targetStr, commasF(mb(totWS)), commasF(mb(totCommit)))
	}
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
		procs[uint32(spi.UniqueProcessID)] = procInfo{
			ppid:    uint32(spi.InheritedFromUniqueProcessID),
			name:    name,
			created: uint64(spi.CreateTime),
			ws:      ws,
			commit:  uint64(spi.PagefileUsage),
		}
		if spi.NextEntryOffset == 0 {
			return procs, nil
		}
		off += uintptr(spi.NextEntryOffset)
	}
}
