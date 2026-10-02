# Key bindings

Riak Commander is keyboard-first; the mouse works for focusing panes,
selecting rows and scrolling. The key bar at the bottom always shows the
keys that work in the current context, and **F1** / **?** opens the help on
the page for what you are doing.

Every function key has an alternative, because many terminals (macOS
Terminal and iTerm2 in particular) intercept F-keys.

## Everywhere

| Key | Alternative | Action |
|-----|-------------|--------|
| `F1` | `?` | help |
| `Tab` / `Shift-Tab` | click | switch between the list and the value pane |
| `s` | `c` | server picker (connect, switch, recent servers) |
| `F10` | `q`, `Ctrl-C` | quit (asks first if there are unsaved edits) |

Letter shortcuts are disabled while you type in a filter or search.

## While a list or value is loading

Opening a bucket type, bucket, key or 2i query switches the pane at once and
shows a spinner, the elapsed time and (for key listings) the number of keys
received so far. Until the data arrives, only these keys work:

| Key | Action |
|-----|--------|
| `Esc`, `←`, `Backspace` | cancel the load and go back to the previous view |
| `F1`, `?` | help |
| `F10`, `q`, `Ctrl-C` | quit |

## List pane (bucket types, buckets, keys)

| Key | Action |
|-----|--------|
| `↑` `↓` `PgUp` `PgDn` `Home` `End`, `j` `k` | move |
| `Enter`, `→` | open the type / bucket / key |
| `←`, `Backspace` | go up a level |
| `/` | filter: type to narrow, `Enter` keeps the filter, `Esc` clears it |
| `Esc` | clear the filter; in 2i results, return to the key list |
| `F5`, `Ctrl-R` | reload |
| `f` | find objects by field (scan or Riak Search) in the open or selected bucket |
| `i` | secondary-index (2i) query on the open or selected bucket |
| `p` | bucket properties of the open or selected bucket |
| `F7`, `n` | new key (key list only) |
| `F8`, `Del` | delete the selected key (asks to confirm); on a remembered bucket type, forget it |

Special rows: **↩ ..** goes up, **+ other bucket type…** opens a type by
name, **↓ load next N keys…** extends a truncated key listing, **→ connect to
a server…** opens the server picker when not connected.

## Value pane: JSON tree

| Key | Action |
|-----|--------|
| `↑` `↓`, `j` `k` | move |
| `Enter` | expand / collapse a container; edit a scalar |
| `→` / `←` | expand / collapse (`←` on a collapsed node jumps to its parent) |
| `*` | expand everything below the selected node |
| `/` | search keys and values; `Enter` or `n` jumps to the next match; `Esc` closes |
| `F4`, `e` | edit the selected field (containers are edited as raw JSON) |
| `F7`, `a` | add a field to the selected object (or an element to an array) |
| `r` | rename the selected object field |
| `F8`, `Del` | remove the selected field or element |
| `F2`, `Ctrl-S` | save the value to Riak |
| `F3`, `v` | switch to the raw JSON text |
| `F5`, `Ctrl-R` | reload from Riak (asks about unsaved edits) |

## Value pane: text, hex and CRDT views

| Key | Action |
|-----|--------|
| `↑` `↓` `PgUp` `PgDn` `Home` `End`, `j` `k` | scroll |
| `F3`, `v` | back to the tree (JSON values only) |
| `F5`, `Ctrl-R` | reload |

## Dialogs

| Key | Action |
|-----|--------|
| `Tab` / `Shift-Tab` | next / previous field |
| `Enter` | confirm (in a single-line field) or press the focused button |
| `Ctrl-S` | confirm from anywhere, including multi-line fields |
| `Esc` | cancel |

## Server picker

| Key | Action |
|-----|--------|
| `Enter` | connect to the selected server, or to the typed address |
| `Ctrl-L` | connect to the most recently used server |
| `e` | label the selected server |
| `d`, `Del` | forget the selected server |
| `↑` `↓` `Tab` | move between the address field and the list |
| any character | (in the list) starts typing a new address |
| `Esc` | close |

## Help

| Key | Action |
|-----|--------|
| `Tab`, `→` / `Shift-Tab`, `←` | next / previous page |
| `1`–`9` | jump to a page |
| `↑` `↓`, `j` `k` | scroll |
| `Esc`, `q`, `?`, `F1` | close |
