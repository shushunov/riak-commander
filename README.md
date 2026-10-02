# Riak Commander

A Midnight Commander-style terminal UI for browsing and **safely** editing
data in a [Riak KV](https://github.com/basho/riak) cluster over its HTTP API.

```
 Riak Commander   ● 10.0.0.5:8098   │  default › users › alice
╭ users › keys ─────────────╮╭ alice ───────────────────────────────────────────╮
│ ↩ ..                      ││  EDITABLE                                         │
│ · alice                   ││ Object  application/json · 412 bytes · vclock ✓  │
│ · bob                     ││ 2i      email_bin = alice@example.com             │
│ · carol                   ││ ───────────────────────────────────────────────── │
│ ↓ load next 1000 keys…    ││ {…} 4 fields                                      │
│                           ││ ├──email: "alice@example.com"                     │
│                           ││ ├──plan: {…} 2 fields                             │
│                           ││ │  ├──name: "business"                            │
│                           ││ │  └──seats: 12                                   │
│                           ││ └──created: "2026-01-02T03:04:05Z"                │
╰───────────────────────────╯╰───────────────────────────────────────────────────╯
 ✔ Loaded alice · 412 bytes · 1 2i index                                   3 keys
 F1 Help  Enter View  ← Back  / Filter  F7 New  F8 Delete  i 2i query  q Quit
```

- **Browse** bucket types → buckets → keys, with incremental filtering.
- **Read** JSON as a collapsible, searchable tree; other text raw, binary as
  a hex dump, CRDTs through the datatypes API.
- **Edit** JSON in place: change, add, rename and delete fields, then save.
  Saves keep the **vclock**, every **secondary index** and user metadata, and
  never reorder keys or round 64-bit numbers.
- **Create and delete keys** (creation never overwrites an existing key).
- **Query secondary indexes (2i)**: exact and range, with index-name suggestions.
- **Resolve siblings** by picking the version to keep.
- **Remember servers**: reconnects to the last server on start; a server
  picker lists recent ones, with labels.
- Dark, light and monochrome themes; mouse support; built-in help (F1 / `?`).

## Install

**Go** (1.24 or newer):

```sh
go install github.com/shushunov/riak-commander@latest
```

**Prebuilt binaries** for macOS, Linux and Windows (amd64/arm64) are attached
to each [GitHub release](https://github.com/shushunov/riak-commander/releases).

**From source:**

```sh
git clone https://github.com/shushunov/riak-commander.git
cd riak-commander
make build          # produces ./riak-commander
```

## Quick start

```sh
riak-commander                       # reconnect to the last server (or pick one)
riak-commander 127.0.0.1             # port defaults to 8098
riak-commander riak.local:8098
riak-commander https://riak.example.com/riak   # TLS and a path prefix (e.g. behind a proxy)
```

No Riak at hand? Start a throw-away node:

```sh
docker run -d --name riak -p 8098:8098 basho/riak-kv
curl -XPUT -H 'Content-Type: application/json' -H 'X-Riak-Index-email_bin: alice@example.com' \
     -d '{"email":"alice@example.com","plan":{"name":"business"}}' \
     http://127.0.0.1:8098/buckets/users/keys/alice
riak-commander 127.0.0.1:8098
```

Then: `Enter` on **default** → `Enter` on **users** → `Enter` on **alice**.
Press **F1** (or `?`) at any time for help about what you are looking at.

## Usage

### Layout

| Area | What it shows |
|------|---------------|
| Header | connection state (● connected, ◌ connecting, ● red unreachable), server, and where you are: `type › bucket › key` |
| Left pane | the list for the current level: bucket types, buckets or keys |
| Right pane | the selected value: a metadata header (content type, size, last modified, vclock, 2i indexes, user metadata, and a badge such as `EDITABLE`, `MODIFIED` or `READ-ONLY`), then the value |
| Status line | messages (✔ success, ▲ warning, ✖ error), a spinner while Riak is busy, otherwise a hint for the current context; counts on the right |
| Key bar | the keys that work right now |

**While something loads**, the pane switches to what you opened right away
and shows a spinner with the elapsed time (and, for key listings, how many
keys have arrived so far). The panes are locked until the data arrives.
**`Esc`** (or `←`) cancels the load and brings back the previous view.

### Connecting and server history

Every server you connect to successfully is remembered. Starting
`riak-commander` with no address reconnects to the most recent one; if that
fails, or on the first run, the **server picker** opens. Press `s` to open it
at any time:

| Key | In the server picker |
|-----|----------------------|
| `Enter` | connect to the selected server, or to the address typed in the field |
| `Ctrl-L` | connect to the most recently used server |
| `e` | label the selected server (e.g. `staging`, `prod – careful`) |
| `d` / `Del` | forget the selected server |
| `↑` `↓` `Tab` | move between the address field and the list |
| `Esc` | close |

Switching servers pings the new one first; you only switch if it answers.

Riak's HTTP API **cannot list bucket types**. The type list always shows
`default`, the types given with `--types`, and every type you have opened on
this server before (via **other bucket type…**). Press `Del` on a remembered
type to forget it.

The history file is `~/.config/riak-commander/history.json` (or
`$XDG_CONFIG_HOME/riak-commander/history.json`; `%AppData%\riak-commander\`
on Windows). It is plain JSON written with `0600` permissions. Addresses with
embedded credentials are refused, so no password ever ends up in it. Use
`--no-history` to neither read nor write it.

### How to…

**Find a key**: open a bucket, press `/` and type part of the key. `Enter`
keeps the filter, `Esc` clears it. To find keys by an indexed value, use a
2i query (below).

**Read a value**: select a key. In the JSON tree, `Enter` or `→` expands,
`←` collapses (or jumps to the parent), `*` expands everything, `/` searches
keys and values (`Enter`/`n` for the next match). `F3` (or `v`) switches to
the raw JSON text.

**Change a field**: select it and press `F4` (or `e`, or `Enter` on a
scalar). Pick the type (string, number, bool, null or raw JSON) and enter the
value. The title gets a ● and the header says `MODIFIED`. **Nothing is sent
to Riak until you press `F2` (or `Ctrl-S`)**. Leaving the key or quitting with
unsaved edits asks whether to save, discard or stay.

**Add, rename or remove fields**: `F7`/`a` adds a field to an object (or an
element to an array), `r` renames an object field, `F8`/`Del` removes the
selected field. All are local edits until you save.

**Create a key**: in a key list press `F7` (or `n`), enter the key, content
type and body. JSON bodies are validated, and the request uses
`If-None-Match: *`, so an existing key is never overwritten.

**Delete a key**: select it in the key list, press `F8` (or `Del`) and
confirm. This is permanent.

**Query a secondary index**: press `i` in a bucket (or with a bucket
selected). Enter the index name (`…_bin` or `…_int`). Names seen on objects
you opened, and names from earlier queries, are suggested. Choose exact or
range and enter the value(s). Results replace the key list; `Esc` returns to
it. `$key` (key-name ranges) and `$bucket` work too. 2i needs a backend that
supports it (leveldb or memory, not bitcask).

**See bucket properties**: press `p` (`n_val`, `allow_mult`, backend,
`datatype`, …).

**Resolve siblings**: opening a key with siblings lists them. Pick one; the
header shows `SIBLING …`; press `F2` to store it as the single resolved value.

### Keys

Many terminals (macOS Terminal and iTerm2 in particular) intercept function
keys, so every F-key has a letter or `Ctrl` alternative.

| Key | Alternative | Action |
|-----|-------------|--------|
| `F1` | `?` | help (opens on the page for the current context) |
| `F2` | `Ctrl-S` | save the open value |
| `F3` | `v` | tree ↔ raw view |
| `F4` | `e`, `Enter` on a scalar | edit the selected field |
| `F5` | `Ctrl-R` | reload the list or the value |
| `F7` | `n` (list) · `a` (tree) | new key · add field |
| `F8` | `Del` | delete key · delete field · forget bucket type |
| `F10` | `q`, `Ctrl-C` | quit |
| `Tab` | mouse click | switch panes |
| `Enter` `→` | | open / expand |
| `←` `Backspace` | | go up / collapse |
| `/` | | filter the list · search the tree |
| `Esc` | `←` while loading | cancel a load · clear filter · leave 2i results · close dialog |
| `s` | `c` | servers |
| `i` | | 2i query |
| `p` | | bucket properties |
| `r` | | rename field |
| `j` `k` | | down / up |

A full reference with every context is in [docs/keybindings.md](docs/keybindings.md).

## Configuration

```
riak-commander [flags] [address]
```

| Flag | Default | Meaning |
|------|---------|---------|
| `--host`, `--port` | | override the host or port of the address (or of `127.0.0.1:8098`) |
| `--max-keys` | `1000` | key-listing page size; listings stream and stop here |
| `--timeout` | `15s` | per-request timeout (key listings get 4×) |
| `--types` | | comma-separated bucket types to always list |
| `--theme` | `dark` | `dark`, `light` or `mono` |
| `--no-history` | | don't read or write the server history |
| `--version` | | print the version |

| Environment | Meaning |
|-------------|---------|
| `RIAK_COMMANDER_THEME` | default for `--theme` |
| `NO_COLOR` | when set (and `--theme` isn't), use the `mono` theme ([no-color.org](https://no-color.org)) |
| `XDG_CONFIG_HOME` | base directory for the history file |

## Safety

Riak Commander exists because editing Riak objects with `curl` is easy to get
subtly wrong. In short:

- **Vclocks**: saves send back the `X-Riak-Vclock` read with the value, so
  concurrent writes are detected instead of silently lost, and the object is
  re-read after saving so the next save is safe too.
- **Secondary indexes and metadata**: Riak stores 2i entries and
  `X-Riak-Meta-*` on the object; a PUT without them deletes them. Every save
  re-sends all of them.
- **Faithful JSON**: key order and number literals are preserved exactly.
- **No accidental overwrite** on create (`If-None-Match: *`).
- **Read-only where writing is unsafe**: CRDTs, binary values and values over
  1 MiB.
- **Bounded listings**: listing keys is a full-cluster scan; listings stream
  and stop at `--max-keys`.

Details: [docs/safety.md](docs/safety.md).

## Limitations

- HTTP API only (port 8098 by default); Protocol Buffers isn't used.
- Bucket types can't be listed over HTTP; enter them once and they're remembered.
- Bucket listings show only buckets that contain keys (a Riak behaviour).
- Listing buckets or keys scans the whole cluster. Fine for development
  clusters, but use with care on large production clusters.
- CRDT values are read-only.
- No authentication beyond what the URL scheme provides (Riak's HTTP
  interface has no auth of its own; put it behind a proxy if needed).

## Troubleshooting

| Symptom | Try |
|---------|-----|
| "Nothing is listening on …" | Is Riak running? Is that the **HTTP** port (default 8098, not the PB port 8087)? Check `listener.http.internal` in `riak.conf`. |
| "No answer … within 5s" | Wrong address, firewall or VPN. |
| "answered, but not like Riak's HTTP API" | Wrong port, or a proxy needs a path prefix: `https://host/prefix`. |
| F-keys do nothing | Use the alternatives in the table above. |
| Colours look wrong | `--theme light`, `--theme mono`, or `NO_COLOR=1`. A terminal with true-colour support looks best. |
| Key listing is slow | Lower `--max-keys`; use a 2i query to find keys instead. |
| 2i query fails mentioning `indexes_not_supported` | The bucket's backend doesn't support 2i (bitcask). |

## Development

```sh
make test      # unit tests + TUI tests on a simulated terminal
make check     # gofmt, vet, staticcheck (if installed), race tests
make build
```

Optional tests against a real cluster (read-only browse, plus a full
create → read → update → delete round trip in a scratch bucket named
`riak_commander_selftest`):

```sh
RIAK_LIVE=127.0.0.1:8098 go test ./... -run Live -v
```

See [CONTRIBUTING.md](CONTRIBUTING.md) and
[docs/architecture.md](docs/architecture.md).

## License

[MIT](LICENSE)
