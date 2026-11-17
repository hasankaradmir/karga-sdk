# karga-sdk

The Go module shared by the [karga CLI](https://github.com/hasankaradmir/karga) and
[karga-server](https://github.com/hasankaradmir/karga-server): the package format, the package
engine and the client/server API between them.

```console
go get github.com/hasankaradmir/karga-sdk
```

| Package | What it holds |
|---|---|
| `resource` | The resource kinds a package renders (Workload, Service, Volume, Network, Config, Secret), their decoding and validation |
| `pkg` | Package metadata (`package.yaml`) |
| `release` | Releases, revisions, applied resources and operations |
| `packages` | The package engine: `Load`, `Lint`, `Render`, `Create` |
| `values`, `renderer`, `archive` | Values merging and JSON Schema, Go templates with Sprig, package archives |
| `plan` | Deployment plans, revision diffs and progress events as the CLI shows them |
| `api` | The API: request and result types, the `Backend` interface, the unix socket `Client` and the HTTP handler (`NewServer`) |
| `logger` | Structured logging with secret redaction |
| `examples` | Reference packages (Redis, Postgres), used by the tests |

## Rendering a package

```go
p, err := packages.Load("examples/redis")
if err != nil { ... }
m, err := packages.Render(p, packages.Options{ReleaseName: "cache", Sets: []string{"image.tag=7.4"}})
if err != nil { ... }
for _, d := range m.Documents { ... }
```

`packages.Lint(path, opts)` returns what is wrong with a package, without a server.

## Talking to karga-server

```go
c := api.NewClient(api.DefaultSocket) // /run/karga/karga.sock
rels, err := c.List(ctx)
res, err := c.Upgrade(ctx, api.UpgradeRequest{Name: "cache", Package: "./redis"})
```

`Client` implements `api.Backend`, as karga-server does. Every request carries the API version
(`api.Major`): a client and a server work together when it is the same, whatever their release
versions, and a mismatch is refused with a message naming the side to upgrade.

## Development

```console
make test lint
```

Changes that alter the API's types or requests are made here first; karga-server and the CLI then
require the new version. Changing `api.Major` breaks every client of an older server.

## License

Apache 2.0, see [LICENSE](LICENSE). Security issues: see [SECURITY.md](SECURITY.md).
