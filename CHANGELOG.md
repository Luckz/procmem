# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.0.0] - 2026-09-17

### Added
- **Process tree aggregation**: Identifies topmost root instances for matching processes and rolls up Private Working Set (RAM MB) and PagefileUsage (Commit MB) across all descendants.
- **Task Manager semantics**: RAM MB metric reflects Private Working Set (`WorkingSetPrivateSize`), preventing shared memory page inflation across multi-process trees.
- **Non-elevated system queries**: Direct integration with `NtQuerySystemInformation(SystemProcessInformation)` for complete visibility across user, system, and service processes without requiring UAC elevation.
- **Root consolidation & cycle detection**: Deduplicates overlapping matching roots and prevents infinite loops on PID 0 or circular parent references.
- **Multi-target queries**: Space-separated process targets (e.g. `procmem chrome discord edge`) evaluated under a unified root hierarchy.
- **Search filtering modes**:
  - Substring search (default)
  - Prefix matching (`/p`)
  - Exact matching (`/e`)
  - Case-sensitivity options (`/c`, `/pc`, `/ec`)
  - Flat process listing (`/f`) without tree rollup
- **Formatted CLI output**: Right-aligned numeric metrics via `tabwriter` with automatic quoting for executable names containing spaces.
- **Optimized build pipeline**: `make.bat` with `-trimpath` and symbol stripping (`-s -w`), plus GitHub Actions multi-arch cross-compilation for `windows/amd64` and `windows/arm64`.
- **Project documentation**: Full architecture specification in `DESIGN.md`, usage guide in `README.md`, and agent instructions in `AGENTS.md`.
