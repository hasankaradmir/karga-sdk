# Changelog

All notable changes are listed here, newest first ([Keep a Changelog](https://keepachangelog.com),
[Semantic Versioning](https://semver.org)).

## [Unreleased]

First release: the code shared by the karga CLI and karga-server moves here from the karga
repository, with its history.

- Package format and engine: resources, package metadata, values with JSON Schema, Go templates
  with Sprig, lint, render, archives, `karga create`'s scaffold; Redis and Postgres examples.
- Plans, revision diffs and progress events.
- The API: types, `Backend`, the unix socket client and the HTTP handler; mutating requests go on
  when the client goes away; API version `1` checked on every request (`Karga-Api` header).
