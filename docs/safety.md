# Safety model

Writing to Riak over HTTP looks simple (`curl -XPUT`), but a naive PUT can
silently lose data. This page explains what Riak Commander does on every
read and write, and why.

## Vector clocks (lost-update protection)

Riak tracks causality with a vector clock, returned as `X-Riak-Vclock` on
every read. A PUT that does not send it back is treated as a write that has
not seen the current value. Depending on the bucket's `allow_mult` setting,
this either creates **siblings** or **silently overwrites** concurrent
changes.

Riak Commander stores the vclock when it reads an object and sends it with
the save. After a successful save it re-reads the object, so the next save
uses the new vclock as well. Deletes send the vclock too when the key was
open.

## Secondary indexes and user metadata

Secondary indexes (`X-Riak-Index-*`) and user metadata (`X-Riak-Meta-*`) are
stored **on the object**, not next to it. A PUT that omits them **removes**
them, which quietly breaks every lookup the application does through that
index.

Riak Commander captures all index and metadata headers on read and re-sends
them on every save. The value header lists the indexes an object carries so
you can see what will be preserved.

## Siblings

When concurrent writes leave conflicting versions (`300 Multiple Choices`),
Riak Commander lists the siblings. Opening one loads it together with the
vclock from the 300 response; saving it (`F2`) writes it as the single
resolved value.

## Faithful JSON

The editor works on a document tree that keeps object key order and the
original text of every number. Saving therefore never reorders a document
and never turns `9007199254740993` into `9007199254740992`, as a round trip
through float64-based JSON libraries would.

## Creating keys

New keys are written with `If-None-Match: *`, so Riak refuses the write if
the key already exists. JSON bodies are validated before sending.

## Read-only on purpose

| Value | Why it's read-only |
|-------|--------------------|
| CRDTs (bucket type with a `datatype` property) | they can only be changed through datatype operations (increment, add to set, update map), not by replacing the value |
| Binary values (invalid UTF-8 or NUL bytes) | shown as a hex dump of the first 4 KiB; a text editor would corrupt them |
| Values over 1 MiB | shown as text; building an editable tree would be slow |
| Non-JSON text | shown as text |

## Expensive operations

Listing buckets and listing keys are full scans of the cluster in Riak.
Riak Commander streams key listings (`?keys=stream`) and stops after
`--max-keys` keys (default 1000), so a mistaken click on a huge bucket does
not read millions of keys. Bucket listings cannot be bounded the same way.
On large production clusters, prefer 2i queries to find keys.

## Nothing happens behind your back

Field edits are local until you press `F2`. Leaving a modified value or
quitting asks whether to save, discard or stay. Deleting a key always asks
for confirmation, and switching servers only happens after the new server
answers a ping.
