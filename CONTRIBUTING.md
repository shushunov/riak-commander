# Contributing

Thanks for helping! Bug reports, ideas and pull requests are welcome.

## Getting started

```sh
git clone https://github.com/shushunov/riak-commander.git
cd riak-commander
make test        # unit tests + TUI tests on a simulated terminal
make run         # build and start
```

You need Go 1.24 or newer. A local Riak is handy for manual testing:

```sh
docker run -d --name riak -p 8098:8098 basho/riak-kv
```

## Before sending a pull request

- `make check` passes (gofmt, vet, staticcheck if installed, race tests).
- New behaviour has a test. UI behaviour can usually be tested like the
  cases in `internal/ui/app_test.go`, which drive the app on a simulated
  screen against a fake Riak.
- User-visible changes are reflected in the in-app help
  (`internal/ui/help.go`), `README.md` / `docs/`, and `CHANGELOG.md`.
- Keep the [safety model](docs/safety.md) intact: writes must keep the
  vclock, 2i indexes and metadata.

## Live tests

Tests named `*Live*` talk to a real cluster and are skipped unless
`RIAK_LIVE` is set. The browse test is read-only; the CRUD test only touches
a scratch bucket called `riak_commander_selftest`.

```sh
RIAK_LIVE=127.0.0.1:8098 go test ./... -run Live -v
```

## Releasing

Maintainers tag a version and push the tag. The release workflow runs
GoReleaser, which publishes binaries for macOS, Linux and Windows:

```sh
git tag v0.1.0 && git push origin v0.1.0
```
