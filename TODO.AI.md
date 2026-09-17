# TODO.AI.md — citylist

Issues found while adding the missing CI/CD workflow set
(`.github/workflows/{ci,docker,daily,release,beta}.yml`). Logged per the
"no issue left only in conversation" rule — none of these were fixed as
part of that task since they were outside its explicit scope (adding
workflows, not fixing existing app/build files).

## 1. Zero Go test coverage — blocks the new `ci.yml` coverage gate

`ci.yml`'s `test` job enforces AI.md's mandatory 60% coverage threshold
via `go test -cover -coverprofile=...` + `go tool cover -func`. This
project currently has no `*_test.go` files, so the first CI run on this
workflow will fail the coverage gate. Needs unit tests written for
`src/` to close the gap.

## 2. LDFLAGS symbol names — AI.md/sibling-repo template vs. actual code

AI.md's generic LDFLAGS template (also used verbatim in sibling repos
like ipgaze) references `main.CommitID`, `main.BuildEpoch`, and
`main.OfficialSite`. citylist's actual `src/main.go` declares only:

```go
var (
    Version   = "dev"
    Commit    = "unknown"
    BuildDate = "unknown"
)
```

There is no `CommitID`, `BuildEpoch`, or `OfficialSite` var — a build
using the template names silently no-ops (Go's `-ldflags -X` doesn't
error on an unknown symbol, it just skips it). All newly-written CI/CD
files (`docker.yml`, `docker/Dockerfile.dev`, `daily.yml`, `release.yml`,
`beta.yml`) were written using the ACTUAL var names
(`main.Version`/`main.Commit`/`main.BuildDate`) instead of the template
names, so they work correctly. Any future edits to these files (or new
ones copied from sibling repos) must preserve this substitution, not
revert to the template names.

## 3. Missing multi-provider workflow set

AI.md's CI/CD requirements (PART 27) call for provider-native workflows
across GitHub/GitLab/Gitea/Forgejo/Jenkins. Only the GitHub Actions set
was added this pass (this repo's remote is github.com/apimgr/citylist).
No `.gitlab-ci.yml`, `.gitea/workflows/`, `.forgejo/workflows/`, or
`Jenkinsfile` exist. Out of scope for the task that added the GitHub
workflows; add if/when this project needs to support those providers.

## 4. Pre-existing `docker/Dockerfile` compliance issues

Discovered while reading the existing production Dockerfile to match its
ARG-naming convention in the new `docker.yml`/`Dockerfile.dev`. Not
fixed — the task was to add missing CI/CD workflows, not rewrite the
existing production Dockerfile.

- **Wrong builder base image**: `FROM golang:alpine AS builder` — AI.md/
  docker-rules.md mandate `casjaysdev/go:latest` for the Go toolchain
  build stage.
- **Baked-in `LABEL` block**: the runtime stage has a full static+dynamic
  `LABEL` block. AI.md forbids this — all OCI metadata must be CI-applied
  only (via `--label`/`--annotation` build args or
  `docker/metadata-action`), never baked into the Dockerfile. (The new
  `docker.yml` added this session does apply labels/annotations
  correctly at build time, but the Dockerfile's own baked-in `LABEL`
  block is now redundant/conflicting and should be removed.)
- **Broken/no-op ldflag**: `-X 'main.CommitID=${VCS_REF}'` references a
  var that doesn't exist in `src/main.go` (see item 2 above) — should be
  `-X 'main.Commit=${VCS_REF}'`.
- **Directory creation in Dockerfile**: creates
  `/config /data/db /data/logs /data/tor /data/geoip /data/backup`
  directly in the Dockerfile. AI.md/docker-rules.md say directory
  creation should be binary-managed at runtime, not baked into the
  image.

## 5. Makefile still references `golang:alpine`

`Makefile` lines 29 and 89 use `golang:alpine` for Docker build
containers instead of the mandated `casjaysdev/go:latest`. Confirmed via
direct grep this session. Not fixed — out of scope for the CI/CD-only
task; flagging for a future pass.

## 6. Additional Makefile/CLI convention gaps (found by go-lint agent)

Flagged by the `go-lint` agent while linting the CI/CD additions this
session. All pre-existing, none fixed — out of scope for the CI/CD-only
task.

- `Makefile` lines 4-5: `PROJECTNAME`/`PROJECTORG` hardcoded instead of
  inferred from `git remote get-url origin`.
- `Makefile` line 13: `LDFLAGS` missing `-trimpath`.
- `Makefile` line 29: `GO_DOCKER` missing `-e GOFLAGS=-buildvcs=false`
  (Git 2.35.2+ rejects a mounted `.git` without it).
- `Makefile` lines 32, 35, 38, 39, 42, 43, 50: `go build` calls missing
  `-buildvcs=false`.
- `Makefile` line 26: `GO_DOCKER` invoked without a preceding
  `@mkdir -p` guard for `GO_CACHE`/`GO_BUILD`.
- `Makefile` lines 41, 43: output binary names use `macos` — should be
  `darwin` (the actual `GOOS` value).
- `Makefile` lines 45, 48: output binary names use `bsd` — should be
  `freebsd` (the actual `GOOS` value).
- `Makefile`: missing a `dev` target (required set: build, release,
  docker, test, dev, clean).
- `src/main.go` lines 45-47: only supports `--version`/`--help` long
  flags — missing `-v`/`-h` short forms.
- `src/main.go`: missing `--debug` flag support.
- `src/main.go`: missing `--color` flag support (`auto`/`yes`/`no`,
  default `auto`).
- `src/main.go`: no `NO_COLOR` environment variable check — output
  should disable colors/emojis when it is set.
