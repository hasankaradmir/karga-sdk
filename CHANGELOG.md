# Changelog

All notable changes are listed here, newest first ([Keep a Changelog](https://keepachangelog.com),
[Semantic Versioning](https://semver.org)).

## [Unreleased]

First release: the code shared by the karga CLI and karga-server moves here from the karga
repository, with its history.

- Package format and engine: resources, package metadata, values with JSON Schema, Go templates
  with Sprig, lint, render, archives; Redis and Postgres examples.
- The API: types, error kinds, `Backend`, the unix socket client and the HTTP handler; mutating
  requests go on when the client goes away.

### Changed

- The SDK holds only the wire contract and the package format. Public packages: `api`,
  `packages` and `resource`; `values`, `renderer` and `archive` are internal.
  - `pkg` is merged into `packages` (`packages.Metadata`); `IsDNSLabel` is in `resource`.
  - `release` and the computing part of `plan` move to karga-server; `plan.Print`/`Summary`,
    `renderer.ToYAML`, `packages.Create` and its scaffold, and `Manifest.Print` move to the CLI.
  - `logger` moves to karga-server.
  - The CLI and the server exchange views: `api.Release`, `api.Revision`, `api.Operation`,
    `api.Step`, `api.AppliedResource`, `api.Plan`, `api.Change`, `api.Event`, …
  - Gateway defaults and settings validation (`DefaultListen`, `DefaultListeners`,
    `Settings.Validate`) and `DefaultTimeout`/`DefaultHistoryMax` move to karga-server.
- API version `2.0`, sent as `Major.Minor`: servers accept clients of any minor version of their
  Major.
- Every request and response field has a JSON name. Values travel as `api.ValuesInput` (file
  contents, `--set`, `--set-string`, strict); `Timeout` travels as text (`"5m0s"`); `HistoryMax` is
  a pointer (nil: the server's default).
- Progress and streams are `Backend` method arguments instead of request fields: `Install`,
  `Upgrade` and `Rollback` take a `progress func(Event)`, `BackupVolume` an `io.Writer`,
  `RestoreVolume` an `io.Reader`. `Uninstall` returns a `*Result` and has `DryRun`.
- `NewServer(b, version, Options)`: `MaxRequest`, `TempDir` and `Classify` replace the global
  `SetStatus`. Errors travel with their kind; status codes are 404, 409, 400 or 500.
- `ErrDetached` and `ErrDisconnected` replace error messages that named CLI commands.
- `archive.Pack` and directory loading skip `.git` and what `.kargaignore` lists.
- A writeOnly value used in a resource other than a Secret (a Workload's environment, a Config
  file) is refused when the package renders: karga stores and shows those specs, and protects
  only Secret values. Put it in a Secret and mount it.

### Fixed

- A template that calls `tpl` on itself crashed the server (stack overflow); `include` and `tpl`
  now share a depth limit, and a render is bounded in time and output size.
- `until`, `untilStep`, `seq`, `repeat`, `indent` and `nindent` could allocate gigabytes.
- Archive directory entries and AppleDouble (`._*`) entries escaped the size and file limits.
- writeOnly values were masked only in Secret resources and NOTES; they are now masked wherever
  they occur in `karga template` output, including under `additionalProperties`,
  `patternProperties`, `items` and `if`/`then`/`else`.
- A volume restore cut short by the client left the volume half overwritten: the backup is now
  received whole first, then restored even if the client goes away.
- A client that stopped reading could stall a deployment on the server.
