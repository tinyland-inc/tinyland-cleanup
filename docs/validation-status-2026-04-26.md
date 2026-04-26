# Validation Status: 2026-04-26

Branch: `main`
Commit: `1ac4953`

## Repo State

- Canonical upstream: `github.com/Jesssullivan/tinyland-cleanup`
- Local checkout: clean `main`, aligned with `github/main`
- Historical fork: `tinyland-inc/tinyland-cleanup`, public and archived
- Open PRs: none
- Open GitHub issues: `#2`, `#3`, `#4`, `#5`, `#6`, `#9`
- Tags/releases: none yet

## Local Validation

Passed locally on 2026-04-26 with repo-managed caches under `/tmp`:

```sh
env GOCACHE=/tmp/tinyland-cleanup-gocache GOFLAGS=-mod=vendor go test ./...
env GOCACHE=/tmp/tinyland-cleanup-gocache GOFLAGS=-mod=vendor go vet ./...
env GOCACHE=/tmp/tinyland-cleanup-gocache GOFLAGS=-mod=vendor go build ./...
```

No cache pruning or destructive cleanup was performed during this validation.

## Hosted CI

Latest `main` CI after PR `#28` passed:

```text
GitHub Actions run 24960795043
Go: passed
Bazel: passed
```

The Bazel hosted job runs through Nix and Bazelisk:

```sh
nix shell nixpkgs#bazelisk --command bazelisk --output_user_root=/tmp/tinyland-cleanup-bazel test //...
```

## GloriousFlywheel Proof

The manual GloriousFlywheel proof workflow is present and configured for
shared-cache-backed Bazel validation, not remote execution/offload. It currently
targets the shared `tinyland-nix` runner class and passes
`BAZEL_REMOTE_CACHE` directly into `scripts/bazel-cache-backed.sh`.

Current blocker:

```text
gh api repos/Jesssullivan/tinyland-cleanup/actions/runners
total_count=0
```

Two workflow-dispatch attempts on 2026-04-25 were cancelled after queueing
without a matching self-hosted runner:

```text
24943163465 cancelled
24943258387 cancelled
```

Next step for `#9`: grant this repository access to an appropriate
GloriousFlywheel `tinyland-nix` runner, confirm the Bazel remote cache endpoint
from that runner, rerun the proof, and record the result here.

## Local Host Snapshot

Read-only disk snapshot during the status check:

```text
/System/Volumes/Data: 31GiB available
/nix:                 31GiB available
```

Nix package and local Bazel validation were not rerun during this status pass to
avoid adding unnecessary store/cache pressure while the machine remains tight.
