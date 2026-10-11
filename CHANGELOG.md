# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).


## [Unreleased]

### Fixed
- **PID reuse in query mode**: Descendant aggregation and root pruning no
  longer attach a process whose recorded parent PID was created *after* the
  process itself. Previously only the upward root walk and the termination
  paths applied the creation-time guard, so a recycled PID could pull an
  unrelated process into a tree and inflate the reported RAM and commit
  totals.

### Changed
- **Single child map**: Both query mode and the termination paths now build
  the parent-to-children map through one `buildKids` helper, unifying tree
  construction across the three traversal paths (query aggregation, kill
  preview, and `collectTree`) and removing two redundant inline creation-time checks.
- **Tests**: Added `main_test.go` covering name parsing and matching, CPU
  time and number formatting, and the grouping logic (rollup, `/x` cut
  points, nested-root pruning, PID-reuse exclusion, `/f` flat mode) on
  synthetic process tables. `make.bat` now runs `go test .` so the suite gates
  every build.

## [1.2.0] - 2026-09-19

### Added
- **Process termination (`/kill`, `/killtree`)**: Terminate individual processes or whole process trees directly from `procmem`, serving as a safer alternative to `taskkill` or Sysinternals `pskill64` with built-in resource reclamation accounting and safety barriers.
  - **/kill `<name|PID>`**: Terminates the specified PID or matching processes without affecting child processes.
  - **/killtree `<name|PID>`**: Terminates matching roots plus all descendant processes top-down (root first, preventing supervisor/watchdog respawns).
  - **Pre-calculated reclamation**: Measures and displays exact physical RAM, commit charge, and CPU time freed upon termination.
  - **Safety barrier**: Terminating by process name strictly requires `/f` or `/force`. Omitting `/f` performs a non-destructive dry run, printing a full preview table of processes and memory before exiting with code 2. Numeric PID targeting does not require `/f`.
  - **PID recycling safety**: Validates kernel process creation timestamps (`child.created >= parent.created`), eliminating PID reuse hazards.
  - **Safety guards**: Rejects empty targets (`""`) and blocks wildcard-only patterns (`*`, `**`) in kill mode to prevent accidental system-wide termination.
  - **Quick usage**:
    - `procmem /kill 1234` — kill single process by PID
    - `procmem /killtree 1234` — kill process tree by root PID
    - `procmem /kill /f notepad` — kill matching processes by name
    - `procmem /killtree /f msedge` — kill browser tree by name
    - `procmem --kill -ef notepad` — kill exact name with flag bundling
    - `procmem /killtree msedge` — dry-run preview (without `/f`)
- **POSIX flag terminator (`--`)**: Supports `--` to stop flag parsing; all subsequent arguments are treated as positional targets.

### Changed
- **Column layout**: Moved `CPU Time`, `GPU Ded`, and `GPU Shr` before `Names` so all fixed-width numeric metrics are grouped on the left, preventing wide process lists from pushing numbers off-screen.
- **Kill dry-run preview**: Includes numeric PIDs in the preview table when mixed with process name targets.

### Fixed
- **Empty string validation**: Disallowed empty process name arguments (`""`), which previously matched every running process on the system.
- **Flag termination with `--`**: Fixed `--` so it properly stops flag parsing per POSIX convention, allowing arguments starting with `-` or `/` after `--` to be targeted as process names.

### Removed
- **Removed `/ec`**: Dropped the deprecated `/ec` option completely, allowing `-ec` to correctly unpack as the short-flag bundle `-e` (exact name) and `-c` (CPU time).

## [1.1.0] - 2026-09-19

### Added
- **GPU memory metrics (`/gpu`, `/g`, `--gpu`)**: Reports Dedicated GPU memory (VRAM) and Shared system GPU memory for matched processes and descendant trees using Windows Performance Data Helper (`pdh.dll`). Evaluated lazily with zero overhead when omitted, requiring zero elevation and zero open process handles.
- **CPU time metrics (`/cpu`, `/c`, `--cpu`)**: Appends total user + kernel CPU execution time formatted as `D:HH:MM:SS`.
- **Short flag bundling & Unix CLI syntax**: Supports POSIX single-hyphen bundles (e.g. `-cgs`, `-xcg`, `-fs`), single flags (`-c`, `-g`, `-s`), and standard GNU-style double-hyphen options (`--cpu`, `--gpu`, `--strict`, `--exact-tree`, `--all`, `--flat`, `--exact`, `--short`, `--help`).
- **Strict tree traversal (`/x`, `/strict`, `--strict`)**: Replaces `/ec` with `/x` ("eXact tree" mnemonic: exact root match and exact-name children only), enabling clean single-letter flag bundling.
- **Short output mode (`/s`, `/short`, `-s`)**: Omits the `Names` column for compact terminal displays.

### Changed
- **Names column ordering**: The root process name is now guaranteed to appear first in the `Names` column, followed alphabetically by unique descendant names.
- **Summary footer**: Displays aggregated CPU time and dedicated/shared GPU memory metrics when enabled.
- **Numeric formatting**: All process counts and memory figures now use thousands separators for readability.
- **Windows slash flags**: Windows slashes are strictly single, space-separated flags (`/c /g /s`) to prevent POSIX path conversion traps in Git Bash and MSYS2 environments.

### Deprecated
- `/ec` is deprecated in favor of `/x` / `/strict` (retained as backward-compatible alias).

## [1.0.0] - 2026-09-17

### Added
- **Process tree aggregation**: Identifies topmost root instances for matching processes and rolls up Private Working Set (RAM MB) and PagefileUsage (Commit MB) across all descendants.
- **Task Manager semantics**: RAM MB metric reflects Private Working Set (`WorkingSetPrivateSize`), preventing shared memory page inflation across multi-process trees.
- **Non-elevated system queries**: Direct integration with `NtQuerySystemInformation(SystemProcessInformation)` for complete visibility across user, system, and service processes without requiring UAC elevation.
- **Root consolidation & cycle detection**: Deduplicates overlapping matching roots and prevents infinite loops on PID 0 or circular parent references.
- **Multi-target queries**: Space-separated process targets (e.g. `procmem chrome discord edge`) evaluated under a unified root hierarchy.
- **Search filtering modes**:
  - Substring search (default)
  - Exact matching (`/e`)
  - Exact tree (`/ec`): exact root and exact-name children only
  - Flat process listing (`/f`) without tree rollup
- **Formatted CLI output**: Right-aligned numeric metrics via `tabwriter` with automatic quoting for executable names containing spaces.
- **Optimized build pipeline**: `make.bat` with `-trimpath` and symbol stripping (`-s -w`), plus GitHub Actions multi-arch cross-compilation for `windows/amd64` and `windows/arm64`.
- **Project documentation**: Full architecture specification in `DESIGN.md`, usage guide in `README.md`, and agent instructions in `AGENTS.md`.
