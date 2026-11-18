# karga-sdk

The Go module shared by the [karga CLI](https://github.com/hasankaradmir/karga-cli) and
[karga-server](https://github.com/hasankaradmir/karga-server). It holds only what both sides must
agree on:

- **the wire contract**: the API types, error kinds and version, and both ends of the protocol;
- **the package format**: what a package is and how it renders, so `karga template` and
  `karga lint` give the same result as an install, without a server.

Behaviour and policy (state, runtime, plans, defaults) live in karga-server; presentation
(printing, prompts, the scaffold of `karga create`) lives in the CLI.

```console
go get github.com/hasankaradmir/karga-sdk
```

| Package | What it holds |
|---|---|
| `api` | Request, result and view types (`Release`, `Revision`, `Operation`, `Plan`, `Event`, …), error kinds, the API version, the `Backend` interface, the unix socket `Client` and the HTTP handler (`NewServer`) |
| `packages` | The package engine: `Load`, `Lint`, `Render`, `Digest`; package metadata (`Package.yaml`); masking of secret values (`Manifest.Redact`, `RedactedDocuments`) |
| `resource` | The resource kinds a package renders (Workload, Service, Volume, Network, Config, Secret), their decoding and validation, `DefaultCaps` |
| `examples` | Reference packages (Redis, Postgres), used by the tests |

Values, templates and archives are internal to `packages`.

## Rendering a package

```go
p, err := packages.Load("examples/redis")
if err != nil { ... }
m, err := packages.Render(p, packages.Options{ReleaseName: "cache", Sets: []string{"image.tag=7.4"}})
if err != nil { ... }
docs, err := m.RedactedDocuments() // secret values replaced by <redacted>
```

`packages.Lint(path, opts)` returns what is wrong with a package, without a server.

Rendering is bounded so an untrusted package cannot exhaust the server that renders it: `include`
and `tpl` nest at most 100 deep, a render takes at most 10 seconds and writes at most 16 MiB, its
`range` loops run at most 1,000,000 iterations together (the time is checked as they run, so a loop
that writes nothing stops too), and `until`, `untilStep`, `seq`, `repeat`, `indent` and `nindent`
refuse huge results, arguments near the limits of an integer included. A package
directory is read without `.git` and without the paths its `.kargaignore` lists; it may hold at
most 10000 files and 32 MiB.

## Talking to karga-server

```go
c := api.NewClient(api.DefaultSocket) // /run/karga/karga.sock
rels, err := c.List(ctx)
res, err := c.Upgrade(ctx, api.UpgradeRequest{Name: "cache", Package: "./redis",
	Values: api.ValuesInput{Set: []string{"image.tag=7.4"}}}, func(e api.Event) { ... })
if errors.Is(err, api.ErrNotFound) { ... }
```

`Client` implements `api.Backend`, as karga-server does. Errors keep their kind across the socket:
`errors.Is` finds `api.ErrNotFound`, `api.ErrConflict`, `api.ErrInvalid` and `api.ErrIncompatible`
on the client side.

Every request carries the API version `Major.Minor` (`api.Version`, header `Karga-Api`). A client
and a server work together when their Major is the same, whatever their release versions. A minor
version only adds endpoints and request fields whose zero value keeps the old behaviour; a newer
client still works with an older server and gets a message to upgrade it for what it lacks.

## Development

```console
make test lint
```

Changes to the API or the package format are made here first; karga-server and the CLI then
require the new version. Changing `api.Major` breaks every client of an older server.

## License

Apache 2.0, see [LICENSE](LICENSE). Security issues: see [SECURITY.md](SECURITY.md).
