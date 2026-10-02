# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project
uses [semantic versioning](https://semver.org/).

## [Unreleased]

### Added

- First public version of Riak Commander.
- Browse bucket types → buckets → keys with incremental filtering and paged
  key listings.
- JSON tree viewer and editor that preserves key order and number literals;
  raw, hex and CRDT views.
- Safe saves: vclock, secondary indexes and user metadata are always
  round-tripped; new keys never overwrite.
- Secondary-index (2i) queries with index-name suggestions and remembered
  queries.
- Sibling resolution.
- Server history: reconnect to the last server on start, server picker with
  labels, remembered bucket types per server.
- Dark, light and monochrome themes (`--theme`, `NO_COLOR`), mouse support,
  context-sensitive key bar and status hints, paged in-app help.
- Addresses may be `host`, `host:port` or `http(s)://host:port/prefix`.
- Visible, cancellable loading: opening a bucket type, bucket, key or 2i
  query switches the pane immediately and shows a spinner, elapsed time and
  live key count; input is locked until the data arrives, and `Esc` cancels
  and restores the previous view.
