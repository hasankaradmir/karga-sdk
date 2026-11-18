# Changelog

All notable changes are listed here, newest first ([Keep a Changelog](https://keepachangelog.com),
[Semantic Versioning](https://semver.org)).

## [Unreleased]

### Added

- `packages.MaskValues` masks the values at secret paths, `*` (every key or element) included,
  for code that stores or shows values.
- `resource.Warnings`, `Manifest.Warnings`: what a package does that is allowed but worth knowing.
  A variable taken from a Secret (`environmentFrom`) is visible to anyone who can inspect the
  container. `packages.Lint` returns these warnings besides the problems.

- `environmentFrom` on a Workload sets variables from Secret values (`{name, secret, key}`);
  `ValidateSet` checks the Secret and the key exist.
- `api/apitest`: `Serve` runs an `api.Backend` behind the real HTTP handler on a unix socket, for
  tests of API clients.

### Changed

- API 3.0 (a new major version: clients and servers of API 2 do not work together).
  - Every endpoint takes its own request type with a `Validate` method, and the server checks it
    before the backend sees it: `ReleaseRequest`, `RevisionRequest`, `ValuesRequest`,
    `DiffRequest`, `ReconcileRequest`, `EventsRequest`, `ExecRequest` and the gateway requests.
    A request no longer accepts the fields of other endpoints.
  - `Backend` is the sum of `Deployer`, `Reader`, `Streamer`, `Reconciler` and `GatewayAdmin`;
    `Remote` adds `Version` and is what `Client` implements.
  - Renamed: `GetValues`, `GetNotes`, `GetManifest` and `Gateway` are `Values`, `Notes`,
    `Manifest` and `GatewayRoutes`. `Pause(name, paused)` is `Pause` and `Resume`.
  - Routes: `/v1/deploy` is `/v1/install`, `/v1/upgrade` and `/v1/rollback`; `/v1/gateway/change`
    is `/v1/gateway/access-log`, `/v1/gateway/listen`, `/v1/gateway/listeners/add` and
    `/v1/gateway/listeners/remove`.
  - `ErrNotRunning`, `ErrDetached` and `ErrDisconnected` are declared with the other errors.
- `resource.Resource.Spec` has the exported type `resource.Spec` (with `Kind()`), which only the
  package implements. `WorkloadSpec.PortNamed` is `TargetPort`: it also matches a port number,
  unlike `ServiceSpec.PortNamed`.
  - The words of a status report are constants (`DesiredApplied`, `ActualMissing`,
    `HealthStarting`, …, and `RevisionSuperseded`), and `StatusReport.Healthy` is the rule `karga
    status` exits 6 by. `ValidateStrategy` checks a `--strategy`; upgrade and rollback requests
    check theirs.
- Built and checked with Go 1.27.4 and govulncheck 1.10.0.

### Fixed

- A Network could use any driver Docker has (`macvlan`, `ipvlan`), putting containers on the host's
  network past the gateway; a Volume any plugin. Networks are `bridge` and Volumes `local` now.
  Variable names may not hold control characters and file names of Configs and Secrets have at most
  255 bytes.
- writeOnly values were missed under `contains`, `unevaluatedProperties`, `unevaluatedItems`,
  `additionalItems`, draft-7 `dependencies` and anchor references (`"$ref": "#name"`); a value
  passed through `b64enc` got into Configs and Workloads, and was not masked. Both walks of a schema
  (secret paths and `--strict`) now know the same keywords.
- Release names are checked by every request, not only when a package is rendered: a name like
  `../x` could reach a path on the server. `resource.ValidateReleaseName` also refuses `karga` and
  `karga-…`, whose runtime objects would take the names of karga's own.
- Errors of logs, volume backups and exec kept only their message: `errors.Is(err,
  api.ErrNotFound)` failed for an unknown release. They keep their kind now.
- `Status`, `Diff`, `Reconcile` and `Uninstall` of `Client` return a nil result with an error.
- Exec no longer leaves a goroutine behind when its context is never canceled.
- A package archive larger than 32 MiB is refused before it is read.
- The API's `Tail`, `TTY` and `Stdin` request fields are omitted when zero, so a newer client of the
  same major version talks to an older server.

- A package could keep the server rendering forever: a nested `range` (over `until`, an integer
  or a value) that writes nothing never reached the time or output limit. Every loop iteration now
  counts (at most 1,000,000 per render) and the time limit is checked as loops run.
- `until`, `untilStep`, `seq`, `repeat`, `indent` and `nindent` got past their size limits with
  arguments near the limits of an integer (`seq -9223372036854775808`) and could exhaust the
  server's memory. They are karga's own now, checking sizes without overflow; their results are
  sprig's for every argument they accept.

## [0.1.0] - 2026-11-18

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

- The package format holds only what karga runs: `runtime.supported` accepts `docker` only, and
  `dependencies` is gone from `Package.yaml` (a package with it is refused as an unknown field).
- A request field or endpoint the server does not know is refused as `ErrIncompatible` ("upgrade
  karga-server") instead of being ignored or reported as not found. `api.Version` is a constant.
- `Backend.BackupState` and `/v1/state/backup` are gone: karga-server keeps its state in files.
- Volume restore uploads are bounded (`Options.MaxRestore`, default 16 GiB).
- Resources are checked more closely when decoded: image references, overlapping `mountPath`s,
  `metadata.name`, `mountOptions` (not supported yet), memory below Docker's 6Mi minimum, and
  quantities that are not plain decimals (`0x10p20`). `resource.DefaultCaps()` is a function.

### CI
- `govulncheck`, and an API compatibility report against the latest release; GitHub Actions are
  pinned by commit.

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
