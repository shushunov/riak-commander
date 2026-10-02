# Architecture

```
main.go                  flag parsing, history loading, starts the UI
internal/riak            HTTP client for Riak KV
internal/jsontree        order- and number-preserving JSON document tree
internal/history         persisted list of recent servers
internal/ui              the terminal UI (tview / tcell)
```

## internal/riak

A small client for the Riak HTTP API: ping, list buckets, stream keys, get,
put and delete objects, get siblings and CRDT values, 2i queries and bucket
properties.

- `NormalizeAddress` turns user input (`host`, `host:port`, `http(s)://…`)
  into a base URL and a short display form. The display form is also the key
  in the server history.
- `RiakObject` carries everything a safe write needs: body, content type,
  vclock, 2i indexes and user metadata (`parseObjectHeaders` /
  `writeObjectHeaders`).
- `ListKeys` reads the `?keys=stream` response incrementally and cancels the
  request once the limit is reached.
- The default bucket type uses the untyped `/buckets/…` URLs; other types use
  `/types/<type>/buckets/…`.

## internal/jsontree

`Parse` builds a tree from JSON tokens with `json.Decoder.UseNumber`, keeping
object members in document order and numbers as their original literal.
`Serialize` writes it back pretty-printed. Editing helpers (`SetScalar`,
`SetRaw`, `AddChild`, `Rename`, `Delete`) mutate the tree in place, and
`Path` renders a JSONPath-like location (`$.plan.seats`) for dialogs.

## internal/history

A JSON file of recently used servers, most recent first, capped at 20. Each
entry also stores bucket types opened on that server (Riak can't list them
over HTTP) and 2i names used per bucket. Writes are atomic (temp file plus
rename) with `0600` permissions. A corrupt file is moved aside instead of
being overwritten.

## internal/ui

| File | Responsibility |
|------|----------------|
| `app.go` | `App`, options, connection handling, the `async` helper, focus, chrome sync |
| `theme.go` | semantic palettes (dark, light, mono) and widget styling helpers |
| `header.go` | top line: connection state and breadcrumb |
| `statusbar.go` | messages with severities, busy spinner, key bar |
| `browser.go` | left pane: types → buckets → keys, filter, paging |
| `viewer.go` | right pane: metadata header, JSON tree, text/hex views, search |
| `editor.go` | field edits, save, key create/delete, sibling picker |
| `indexquery.go` | 2i query dialog and index suggestions |
| `servers.go` | server picker and connection error messages |
| `keys.go` | global key dispatch, key hints, context hints, bucket properties |
| `help.go` | paged in-app help |
| `modals.go` | dialog plumbing: centred frames, forms, confirm and choice |

### Threading model

All widget state belongs to tview's event goroutine. Network calls go
through `App.async`. It runs the call on a worker goroutine with a timeout,
then applies the result back on the event goroutine with `QueueUpdateDraw`.
Only one call is in flight at a time, which keeps pane state simple.

In tview v0.42, `QueueUpdate` / `QueueUpdateDraw` **block** until the event
loop runs the function. Never call them from the event goroutine itself (for
example inside a key handler or a dialog callback): it deadlocks.

### Chrome

The header, pane borders, selection styles, key bar and status hint are
derived from state in a before-draw hook (`App.syncChrome`), so no code path
can forget to refresh them. The hook runs while tview holds its application
lock, so it must not call `Application` methods. It uses `HasFocus` on the
primitives instead of `GetFocus`.

### Colours

Code never uses colour names directly: it asks the active `Theme` for a role
(accent, muted, danger, JSON key…). Selected rows are drawn without colour
tags so the selection style applies to the whole row.

### Tests

`internal/ui/app_test.go` runs the real application on a tcell simulation
screen against an `httptest` fake of Riak, injecting key presses and reading
the screen back. It covers browsing, editing and saving (checking that the
vclock and 2i headers are sent), the history-driven reconnect, connection
errors, remembered bucket types and the help screen.
