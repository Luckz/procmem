# AGENTS.md

`procmem` is a single-file Windows CLI utility in [main.go](main.go). It finds processes matching an input name, identifies each root instance (topmost matching ancestor), and aggregates RAM (working set) and commit (`PagefileUsage`) for each root plus **all of its descendant processes** by default (and optional additional metrics).

- **Architecture & Invariants**: Consult [DESIGN.md](DESIGN.md) before making changes. It details root discovery, descendant traversal, memory semantics, and Windows API quirks.
- **CLI & Usage**: See [README.md](README.md) for flag semantics and exit codes. Options strictly use Windows forward slashes (`/flag`), never hyphens (`-flag` / `--flag`), because process executable names frequently contain hyphens (e.g. `ms-teams.exe`, `code-insiders.exe`).
- **Build & Verification**: Run `make.bat` (executes `go vet .` and builds `procmem.exe`).
