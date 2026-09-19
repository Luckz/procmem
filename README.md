# procmem

Per-root-instance memory totals for a named process on Windows.

`procmem` finds every process whose name matches the given input, groups
each under its topmost matching ancestor (the *root instance*), and prints
the combined RAM (working set) and commit of each root plus **all of its
descendant processes** — e.g. `steam.exe` together with its whole
`steamwebhelper` tree, or a browser with its helper and handler children.

```text
C:\> procmem notepad
  RootPID  Procs  RAM MB  Commit MB  Names
    17276      1     2.8      129.7  notepad++.exe
    57176      1     2.2       73.8  notepad4.exe
   114044      1     1.3       73.0  notepad4.exe
    37240      1     1.5       70.4  notepad4.exe
    27660      1     1.4       13.2  notepad2.exe
    46152      1     0.1        9.5  notepad.exe
=> 6 root(s), 6 process(es) matching 'notepad': RAM 9.3 MB, commit 369.6 MB
```

## Usage

```
procmem [/a | /x | /f] [/e] [/c] [/g] [/s] <name>...

  /a   /all      count all descendants regardless of name (default)
  /x   /strict   eXact tree (implies /e); count only descendants
                 named exactly like the root
  /f   /flat     one row per matching process; no grouping, no rollup
  /e   /exact    <name> is an exact process name, .exe suffix optional
  /c   /cpu      show total CPU time (User + Kernel)
  /g   /gpu      show Dedicated and Shared GPU memory
  /s   /short    omit the Names column
  /h   /?  /help show this help
```

Options can use Windows slashes (`/flag`), Unix hyphens (`-flag`, `--flag`),
and short-flag bundling (e.g. `-cgs`, `-xcg`, `-ef`). Multiple process
names can be provided to query several targets together, e.g.
`procmem brave chrome msedge`.

Copy `procmem.exe` to a folder in your `%PATH%` (e.g. `%USERPROFILE%\.local\bin\`)
for quick access from any Command Prompt or PowerShell window.

## Matching

- `steam.exe` — input ending in `.exe` is an **exact** name match.
- `steam` — anything else is a **substring** match (case-insensitive)
  against process names.
- `*team*` — an explicit `*` forces substring matching; the `*` characters
  and a trailing `.exe` are stripped first, so this matches `steam.exe`
  and would match a hypothetical `ms-teams.exe`.
- `/e steam` — force exact matching for suffix-less input (`.exe` is
  matched or appended automatically, also matching extensionless system
  processes like `System` or `Registry`).
- `/x explorer` (or `/strict`) — eXact tree: implies `/e` and counts only
  descendants named exactly like the root: counts `explorer.exe` instances
  alone, ignoring differently-named child processes.
- `/f discord` — flat listing: every matching process as its own row, no
  grouping and no descendant rollup.
- `/cpu firefox` (or `/c`) — appends a `CPU Time` column reporting total
  execution time (user + kernel across all threads) formatted as `[D:]HH:MM:SS`.
- `/gpu msedge` (or `/g`) — appends `GPU Ded` and `GPU Shr` columns
  reporting dedicated and shared GPU memory in MiB.
- `code note` — matches any process whose name matches either query
  (e.g. VS Code and Notepad, but also OpenCode and Notepad4);
  roots are aggregated and sorted together into a single table.

A process becomes a group's *root* when its parent does not match, is
gone, or fails the parent-PID-reuse check (a recorded parent created
*after* the child cannot be its real parent). That keeps nested same-name
trees attached to their true topmost ancestor.

## Process termination

```
procmem /kill [/f] [/e] [/g] [/s] <name|PID>...
procmem /killtree [/f] [/x] [/g] [/s] <name|PID>...

  /kill          terminate matching process(es) or <PID>
  /killtree      terminate matching process(es) or <PID> plus all descendants
  /f   /force    force termination; required when killing by process name
  Options /e, /x, /g, and /s also apply (/s gives a compact list).
```

`procmem` can terminate individual processes or whole process trees using the Windows `TerminateProcess` kernel API. Before terminating, it calculates and reports the exact resources freed (RAM, commit, and CPU / optional GPU memory). CPU time is always included on the termination path.

```text
C:\> procmem /kill 44936
Killed PID 44936  ONENOTE.EXE
=> Killed PID 44936 (ONENOTE.EXE): freed 91.8 MB RAM, 166.1 MB commit, CPU 00:00:06
```

- **/kill `<name|PID>`** — terminates the specified PID or matching processes (flat: descendants are not terminated).
- **/killtree `<name|PID>`** — terminates the specified PID or matching root processes **plus all of their descendant processes**.
  - Terminations are executed **top-down (root first)**: the root process is terminated immediately so supervisor/watchdog processes cannot detect child death and respawn replacement children.
  - Unlike Windows `taskkill /T`, `procmem` verifies process creation timestamps (`child.created >= parent.created`), preventing accidental termination if PIDs are recycled.
  - CPU time is always included on the termination path without requiring a flag.
- **Safety barrier for process names**: When terminating by process name (rather than a numeric PID), **`/f` or `/force` is strictly required** as a safety confirmation. If omitted, `procmem` refuses to kill, displays a rich dry-run preview table with RAM, commit, and CPU time (listing every child executable on extra lines under `Name` for `/killtree`), and exits with code 2:
  ```text
  C:\> procmem /killtree nvcontainer office
  procmem: killing by process name requires /f or /force (e.g. procmem /killtree /f nvcontainer office)

  RootPID  Procs  Name                  RAM MB  Commit MB  CPU Time
     4164      3  nvcontainer.exe         18.3      120.9  01:37:10
    78628      2  OfficeClickToRun.exe     5.2       30.8  00:00:18
                  AppVShNotify.exe    
  => 2 root(s), 5 process(es) matching 'nvcontainer, office': RAM 23.6 MB, commit 151.6 MB, CPU 01:37:29

  Use /f to confirm: procmem /killtree /f nvcontainer office
  Or target a single PID: procmem /killtree 4164
  Use /e /f (or --kill -ef) to restrict to exact process names.
  ```
  *(Pass `/s` or `-s` for a compact, de-verbosed list).*
- **Targeting by PID** (`procmem /kill 1234` or `procmem /killtree 1234`): Numeric arguments are recognized as PIDs and do not require `/f`.
- **Options and bundling**:
  - `procmem --kill -ef notepad` — exact process name match + force.
  - `procmem --killtree -xf msedge` — exact root and exact descendants + force.
  - `procmem /kill -s 1234` — compact output (omits per-PID output, prints summary only).

## The columns

| Column | Meaning |
|---|---|
| RootPID | PID of the group's root (topmost matching ancestor) |
| Procs | number of processes counted in the group |
| RAM MB | private working set of the group, in MiB (Task Manager's default "Memory" column) |
| Commit MB | commit charge of the group (psapi `PagefileUsage` = Task Manager's "Commit size") |
| Names | unique process names in the group (hidden by `/s`), with the root name listed first; names with spaces are quoted |
| CPU Time | (optional: `/cpu` or `/c`) total CPU execution time across all threads in `[D:]HH:MM:SS` |
| GPU Ded | (optional: `/gpu` or `/g`) dedicated GPU memory in MiB |
| GPU Shr | (optional: `/gpu` or `/g`) shared system GPU memory in MiB |

Rows are sorted by commit, highest first.

With `/f`, each matching process gets its own row under a `PID` header and
the `Procs` column is omitted.

Two things to know about the numbers:

- **RAM MB is the private working set** (physical RAM holding private,
  non-shareable pages, matching Task Manager's default "Memory" column).
  Unlike total working set, private working set cleanly sums across trees
  without inflating or double-counting shared DLLs and memory-mapped files.
- Counts and MB values fluctuate from run to run while the target app
  spawns or exits short-lived helper processes. That is real process
  churn, not a measurement bug.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | success (including `/h` or all targeted processes terminated) |
| 1 | snapshot failed, nothing matched, or at least one process failed to terminate |
| 2 | usage error (unknown option, missing `/f` for name termination, conflicting options) |

## Notes & limitations

- **No elevation is required for inspection.** `procmem` queries process and
  memory metrics via `NtQuerySystemInformation(SystemProcessInformation)`,
  which the kernel serves without opening process handles. Service-hosted
  and system processes (e.g. `svchost.exe`, `WmiPrvSE.exe`, `System`) are
  fully readable from a normal, non-elevated user token.
- **Process termination permissions**: Terminating processes running in your own
  user session does not require elevation. Terminating processes owned by other
  users or elevated system services requires running `procmem` from an elevated prompt.
- Windows 10 or later (a requirement of the Go toolchain used to build;
  the underlying APIs go back further).
- Git Bash / MSYS users: the shell rewrites leading-slash options into
  paths (`/e` becomes `E:\`). Prefix invocations with
  `MSYS_NO_PATHCONV=1 MSYS2_ARG_CONV_EXCL='*'`, or use cmd.exe /
  PowerShell.

## Building

Requires a Go toolchain (go.mod declares `go 1.26.0` as the minimum); the
only dependency is `golang.org/x/sys`. Run `make.bat` (vet + build), or
manually:

```
go build -trimpath -ldflags="-s -w" -o procmem.exe .
```
