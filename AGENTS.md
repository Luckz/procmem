# AGENTS.md

`procmem` is a single-file Windows CLI utility in [main.go](main.go). It finds processes matching an input name, identifies each root instance (topmost matching ancestor), and aggregates RAM (working set) and commit (`PagefileUsage`) for each root plus **all of its descendant processes** by default (and optional additional metrics).

- **Architecture & Invariants**: Consult [DESIGN.md](DESIGN.md) before making changes. It details root discovery, descendant traversal, memory semantics, and Windows API quirks.
- **CLI & Usage**: See [README.md](README.md) for flag semantics and exit codes. Options support Windows forward slashes (`/flag`), Unix hyphens (`-flag`, `--flag`), bundling (e.g. `-cgs`, `-xcg`, `-ef`), and process termination (`/kill`, `/killtree`).
- **Build & Verification**: Run `make.bat` (executes `go vet .` and builds `procmem.exe`).
