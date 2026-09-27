# AGENTS.md — AdGuard Home (Go backend + two frontends)

Repo root for AdGuard Home. Go 1.27 backend under `internal/`; two frontends:
`client/` (legacy React 16) and `client_v2/` (SolidJS, the new UI).

## Commands (via `make` — all wrap `scripts/make/*.sh`)

- Full build: `make build` (= `make deps quick-build`). Quick rebuild: `make go-build` / `make js-build`.
- Backend checks (what CONTRIBUTING.md wants for backend): `make go-check` (= `go-lint go-test`).
- Test only: `make go-test` — runs the whole module with **race detector on, `--count=2`, `--shuffle=on`, 90s per-package timeout, coverage** (`cover.out`). It always forces `RACE=1`, so it needs cgo. For a single package use plain `go test` (the script's flags are for full runs, not per-package iteration).
- Single test: `go test -run 'TestFoo' ./internal/filtering/...` — do this for fast iteration; the `make` targets are all-module. `make go-bench` and `make go-fuzz` are separate targets (CI runs them alongside `make test`).
- Lint only: `make go-lint` (gofumpt, go vet, staticcheck, errcheck, gosec, ineffassign, shadow, unparam, misspell, gocyclo, gocognit, fieldalignment, nilness, govulncheck + custom checks). `EXIT_ON_ERROR=0` shows all errors instead of failing fast.
- Frontend: `make js-lint` / `make js-typecheck` / `make js-test` / `make js-test-e2e` (playwright). Inside `client_v2/` you can also `npm run check` (lint+typecheck+test) or `npm run dev`.
- Git hooks: `make init` (sets `core.hooksPath=./scripts/hooks`). `pre-commit` warns on `FIXME`/`TODO.*!!` and lints staged changes.
- Verbosity: prefix any target with `VERBOSE=1` (or `=2`). `make go-env` dumps the toolchain.

**Order that matters:** for backend changes run `make go-check` (lint then test). For frontend run `make js-lint` (and/or `client_v2/npm run typecheck`). Run `go-deps` after editing `go.mod`/`go.sum`.

## Go specifics an agent gets wrong

- **`work` is the package pattern.** `go-test.sh` runs `go test … work`, and `go-lint.sh` passes `work` to vet, staticcheck, ineffassign, etc. The bare word `work` is a Go package pattern matching every package in the main module (equivalent to `./...`; 43 pkgs here). Don't "fix" it to `./...` and don't create a `work/` directory.
- **Lint bans these imports** (enforced by `blocklist_imports` in `go-lint.sh`): `errors` (use `golibs/errors`), `log` (use `log/slog` + `golibs/logutil/slogutil`; only `home`, `dhcpd`, `aghos` may use `log`), `sort` (use `slices`), `reflect`, `unsafe` (except `internal/permcheck/security_windows.go`), `io/ioutil`, `x/exp/maps`, `x/exp/slices`, `x/net/context`, and `promauto`. New exceptions must be documented in the comment there.
- **File naming:** no underscores in Go filenames (a `make` check enforces this) except allowed suffixes (`_test`, `_linux`, `_windows`, `_darwin`, `_bsd`, `_unix`, `_freebsd`, `_openbsd`, `_next`, `_others`, `_generate`).
- **HTTP methods must be named constants**, not raw `"GET"`/`"POST"` strings (a lint check greps for these).
- **Formatting is `gofumpt --extra`** (stricter than gofmt). Also enforced: gocyclo ≤ 10, and per-package gocognit/fieldalignment/gosec thresholds differ by package (see `go-lint.sh` for the exact lists).
- **Testing convention:** internal tests are named `*_internal_test.go` (dominant, ~97 files); external `*_test.go` exist too. Test helpers live in `internal/aghtest` (e.g. `aghtest.StartHTTPServer`, `StartLocalhostUpstream`).
- **Build injects version via ldflags** into `internal/version` (`version`, `channel`, `committime`, `goarm`, `gomips`); PGO uses the committed `default.pgo` (`--pgo=auto`). Don't hand-edit those values.

## Frontend: two clients, pick correctly

- `Makefile` sets `CLIENT_DIR = client_v2` (SolidJS). But the **test CI workflow overrides it to `client`** (the legacy React app), and `build.yaml` defaults to `client_v2`. So `client/` is legacy/being phased out, `client_v2/` is the new UI. Don't assume `client/` is the current UI.
- Frontend builds emit into `build/` (gitignored except `gitkeep`), which is **embedded into the Go binary** via `//go:embed build` in `main.go`/`main_next.go`. So a full Go build depends on the frontend build having populated `build/`.
- `client_v2` has its own extensive, more specific `AGENTS.md` + `DEVELOPMENT.md` — read them for frontend work (translations, toasts, the `panel/*` alias, vitest, etc.). Root file does not duplicate those.
- README.md still documents the legacy `client/` dev flow; `client_v2/DEVELOPMENT.md` is the accurate frontend guide.

## Two backends behind a build tag

- Default build: `internal/home` (the full, stable AdGuard Home). Build tag `next` selects the experimental rewrite `internal/next` (its own `cmd`, `websvc`, `configmgr`, `dnssvc`).
- Switch with `NEXTAPI=1` (Makefile) → `--tags=next`; `main_next.go` is `//go:build next`. `internal/next/AdGuardHome.example.yaml` is its sample config.

## Conventions

- Commits use conventional prefixes, e.g. `fix(client_v2): ...`, `AGH-31 ...`, `all: ...`, `AGDNS-3720 ...` (the leading token is often an issue key). `changelog.config.js` defines the (external) changelog-tool vocabulary: types `+`/`*`/`-` and scopes `ui`, `global`, `filtering`, `home`, `dnsforward`, `dhcpd`, `querylog`, `documentation`; `CHANGELOG.md` is maintained by hand under `## [Unreleased]`.
- Go code guidelines (commenting, naming, testing, git, shell) are centralized in the external AdGuard [CodeGuidelines](https://github.com/AdguardTeam/CodeGuidelines) repo — `HACKING.md` here is only links to it.
- Go tooling is module-managed via `go.mod` `tool` directives — run linters as `go tool <name>` (e.g. `go tool gofumpt`), not global installs.
- Shell scripts under `scripts/` are POSIX `sh` (`set -e -f -u`, `##` comments); they set `AdGuard-Project-Version:` markers to simplify syncing across copies.
- Cross-platform: `make go-os-check` vets for darwin/freebsd/openbsd/windows and 386/amd64 — run it when touching OS-specific code.

## Gotchas

- `GOTOOLCHAIN` in the Makefile matches `go.mod` (`go 1.27.1`). If `make` tries to fetch a toolchain, run targets with `GOTOOLCHAIN=local`.
- The release/test pipeline runs `make deps test go-bench go-fuzz`; `go-test` uses a fixed 90s per-package timeout — very slow integration tests can need `TIMEOUT_FLAGS`.
- Do not edit `go.mod` deps by hand for tooling; add to the `tool` block and run `go mod tidy`.
- Generated/ignored dirs (`build/`, `dist/`, `data/`, `tmp/`, `node_modules/`, `*.test` binaries) are build outputs, not source.
