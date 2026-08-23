# Repository Instructions

## Release policy

This repository contains multiple independent Go modules. Version and release
each module independently. Do not treat matching versions across the repository
as a requirement.

### Decide which modules to release

- Release a module only when that module has changed since its latest tag.
- Before preparing a release, compare every candidate module with its own latest
  tag and report the modules that actually changed.
- Changes owned by the root module, including the embedded UI under `ui/`,
  require only a root `vX.Y.Z` release unless a nested module also changed.
- A change confined to a nested module requires only that module's release.
- Do not edit nested `go.mod` files or publish nested modules merely to keep
  their version numbers aligned with the root module.
- A nested module may keep requiring an older compatible root version. Go's
  Minimal Version Selection will use a newer root version when the consuming
  application requires one.
- Release a nested module when its own code, public API, behavior, or necessary
  dependency constraints changed.

### Versioning and tags

- Apply Semantic Versioning independently to every module.
- Use `vX.Y.Z` for the root module.
- Use `<module-path>/vX.Y.Z` for nested modules, for example
  `profiler/pgx/v0.4.0` or `cmd/webpprof-mcp/v0.4.0`.
- Never reuse, move, or delete a published Go module tag. The Go module proxy
  may already have cached it.
- Do not create commits, tags, pushes, or GitHub releases unless the user has
  explicitly requested those actions.

### Release checks

Before tagging any module:

1. Confirm the worktree contains only the intended release changes.
2. Run `./scripts/modules.sh tidy` and verify that it introduces no unexpected
   `go.mod` or `go.sum` changes.
3. Run `make check`.
4. Run `./scripts/modules.sh test -race -shuffle=on -count=1`.
5. Run `govulncheck ./...`, or the equivalent official Go vulnerability check.
6. Update `CHANGELOG.md` for the root release or the relevant module release
   notes.

### Publishing

- Push only the tags for modules selected by the changed-module review.
- Prefer pushing release tags individually. GitHub may not emit tag push events
  when many tags are pushed in one operation.
- Verify every published module version through `proxy.golang.org`.
- Publishing a nested profiler, storage, or command module means pushing its
  module-scoped tag and verifying the version through `proxy.golang.org`. Do
  not create a GitHub Release for nested modules.
- The `cmd/webpprof-mcp` module follows the same rule: publish
  `cmd/webpprof-mcp/vX.Y.Z` as a Go module tag, but do not create a separate
  GitHub Release for the MCP binary.
- Create GitHub Releases only for root `vX.Y.Z` tags. The root
  `webpprof vX.Y.Z` GitHub Release must be marked as **Latest** and verified
  through the GitHub API.
