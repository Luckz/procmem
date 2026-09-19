# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
