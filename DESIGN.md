# procmem — design notes

Internal design and implementation notes. User-facing documentation
lives in [README.md](README.md).

procmem groups every process matching the input under its topmost matching
ancestor (the "root instance") and prints per-group RAM (working set) and
commit (PagefileUsage) totals plus the unique process names involved.

## Design decisions

- **Traversal scopes — three mutually exclusive modes**: the default (and
  `/a /all`) counts the entire descendant subtree regardless of name — a
  game client without its webhelper tree, or a browser without its helper
  children, is not a useful total. `/ec` restores same-name-only counting
  with cut-point traversal (non-matching children are neither counted nor
  traversed through) and implies exact input matching. `/f /flat` lists
  each matching process as its own row: no upward grouping, no descendant
  rollup, `PID` header, no `Procs` column.
- **Substring matching is the default input mode.** A `.exe` suffix, `/e`
  or `/ec` opt into exact matching; an explicit `*` opts back out. In fuzzy
  mode `*` characters and a trailing `.exe` are stripped before the
  substring match, so `*team*` matches `steam.exe` and would match a
  hypothetical `ms-teams.exe`.
- **Root selection**: topmost ancestor whose own name also matches the
  input. Parent PIDs and creation times are obtained directly from the kernel
  process snapshot; the guard stops the upward walk when the recorded parent
  was created *after* the child (a reused PID cannot be the real parent). A
  seen-set plus a 128-step cap protect against cycles and pathological chains.
  Additionally, any root that is reachable as a descendant of another root
  under the active traversal mode is pruned, preventing double-counting when
  a matching process sits under a non-matching intermediate.
- **No elevation is required or used.** Because data is acquired through a
  single `NtQuerySystemInformation(SystemProcessInformation)` query rather
  than opening per-process handles, Win32 object manager DACLs are never
  consulted. Services and system processes (`svchost.exe`, `WmiPrvSE.exe`,
  `System`) are fully readable from a non-elevated token.
- **Multiple process targets**: Multiple names can be passed (e.g.
  `procmem chrome steam`). Each name is parsed with its own matching rules
  (exact vs substring). In tree mode, `rootOf` walks up ancestors matching
  *any* of the queries (Option A: shared root group), preventing fragmented
  or duplicated subtrees if one queried process spawned another.
- **Process names with spaces and pseudo-processes**: Names in the `Names`
  column are quoted if they contain spaces (e.g. `"memory compression"` or
  `"Epic Games Launcher.exe"`), preserving standard single-space token
  separation without ambiguity. Under exact matching (`/e` or `/ec`),
  suffix-less inputs match either `<name>.exe` or exact `<name>`, enabling
  exact matching of extensionless Windows system pseudo-processes (`System`,
  `Registry`).

## Number semantics

- **Private Working Set (`WorkingSetPrivateSize`)**: Physical RAM holding
  private, non-shareable pages, matching Task Manager's default "Memory" column.
- **Commit Size (`PagefileUsage`)**: Equals Task Manager's "Commit size". WMI's
  `Win32_Process.PageFileUsage` is the same quantity in KB; the kernel
  reports bytes.
- **Why Total Working Set (`WorkingSetSize`) is rejected**: Total working set
  includes shareable pages (executable code of shared DLLs like `kernel32.dll`,
  app runtimes, and memory-mapped files) that exist in physical RAM only once.
  Summing total working set across an $N$-process tree counts those shared
  pages $N$ times, producing phantom memory totals that can easily exceed
  installed physical RAM (e.g. hundreds of browser processes reporting 50+ GB of
  RAM on a 32 GB machine). Because total working set is mathematically
  non-additive across process groups, it was intentionally rejected. Both
  Private Working Set and Commit are strictly additive, ensuring table rows and
  summary totals represent real, uniquely attributed memory without disclaimers.
- **Why Page Frame Number (PFN) set union is rejected**: In theory, querying
  raw physical Page Frame Numbers across a tree and calculating their set union
  would capture intra-tree shared pages while deduplicating them. This was
  rejected because:
  1. *Elevation barrier*: Reading raw physical page mappings requires
     administrative rights or a kernel driver (`PROCESS_VM_READ` / `SeDebugPrivilege`),
     violating the hard no-elevation design invariant.
  2. *Severe performance penalty*: Instead of reading kernel-computed counters
     in a single ~10 ms syscall, inspecting millions of individual 4 KB pages
     via `QueryWorkingSetEx` across dozens of processes spikes latency from
     milliseconds to seconds.
  3. *Attribution flaw*: Even a PFN union captures system-wide DLLs (`ntdll.dll`,
     `kernel32.dll`), so it still fails to answer how much memory killing the
     tree will actually release. Private Working Set provides ~98% of the
     practical truth with virtually zero overhead.

## Windows API architecture (why the code looks like this)

- **Why `NtQuerySystemInformation` instead of Toolhelp32 + `OpenProcess`**:
  Win32 `OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION)` is denied by DACLs
  on service-hosted and protected processes (e.g. `svchost.exe`, `WmiPrvSE.exe`,
  `steamservice.exe`) when running non-elevated, producing unreadable rows.
  Calling `NtQuerySystemInformation(SystemProcessInformation)` queries the
  kernel directly. The kernel fills process IDs, parent PIDs, creation
  timestamps, image names, and memory counters for every process across all
  sessions in one system call without checking process DACLs.
- **Dynamic buffer management**: `SystemProcessInformation` buffer size cannot
  be determined statically. The query allocates an initial 2 MB buffer. If the
  call returns `STATUS_INFO_LENGTH_MISMATCH` (`0xC0000004`), it reallocates
  using the kernel's returned length plus 256 KB headroom to absorb process and
  thread churn between attempts, or doubles the buffer if the returned length
  is unpopulated.
- **Dependency on `x/sys/windows`**:
  `x/sys/windows` exports `windows.SYSTEM_PROCESS_INFORMATION` and
  `windows.NTUnicodeString`, but does not export an `NtQuerySystemInformation`
  call wrapper. We load it via `windows.NewLazySystemDLL("ntdll.dll").NewProc("NtQuerySystemInformation")`.
