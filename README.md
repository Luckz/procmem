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
procmem [/a | /ec | /f] [/e] [/cpu] [/gpu] [/s] <name>...

  /a   /all      count all descendants regardless of name (default)
  /ec            exact root name (implies /e); count only descendants
                 named exactly like the root
  /f   /flat     one row per matching process; no grouping, no rollup
  /e   /exact    <name> is an exact process name, .exe suffix optional
  /cpu /c        show total CPU time (User + Kernel)
  /gpu /g        show Dedicated and Shared GPU memory
  /s   /short    omit the Names column
  /h   /?  /help show this help
```

Options are case-insensitive and can be combined, e.g.
`procmem /s /ec chrome.exe`. Multiple process names can be provided to
query several targets together, e.g. `procmem brave chrome msedge`.

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
- `/ec explorer` — implies `/e` and additionally counts only descendants
  named exactly like the root: counts `explorer.exe` instances alone,
  ignoring differently-named child processes.
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
| 0 | success (including `/h`) |
| 1 | process snapshot failed, or nothing matched |
| 2 | usage error (unknown option, more than one of `/a` `/ec` `/f`, wrong argument count) |

## Notes & limitations

- **No elevation is required or used.** `procmem` queries process and
  memory metrics via `NtQuerySystemInformation(SystemProcessInformation)`,
  which the kernel serves without opening process handles. Service-hosted
  and system processes (e.g. `svchost.exe`, `WmiPrvSE.exe`, `System`) are
  fully readable from a normal, non-elevated user token.
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
