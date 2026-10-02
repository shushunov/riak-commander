package riak

import (
	"net/http"
	"strings"
)

// RiakObject is a single KV object together with the metadata that must be
// round-tripped on writes: the vclock (lost-update protection) and the
// X-Riak-Index-* secondary indexes (a PUT without them destroys the 2i
// entries applications look objects up by, e.g. email_bin).
type RiakObject struct {
	BucketType  string // "" or "default" means the untyped /buckets/... form
	Bucket      string
	Key         string
	Body        []byte
	ContentType string
	Vclock      string              // opaque X-Riak-Vclock
	Indexes     map[string][]string // index name (lowercase, incl. _bin/_int suffix) -> values
	Meta        map[string]string   // X-Riak-Meta-* (name lowercased, without prefix)
	LastMod     string
	ETag        string
}

const (
	indexPrefix = "x-riak-index-"
	metaPrefix  = "x-riak-meta-"
)

// parseObjectHeaders extracts vclock, 2i indexes and user meta from a GET/HEAD
// response. Go canonicalizes header names (X-Riak-Index-Email_bin), so
// matching is case-insensitive and captured names are lowercased — Riak index
// names are lowercase on the wire.
func (o *RiakObject) parseObjectHeaders(h http.Header) {
	o.Vclock = h.Get("X-Riak-Vclock")
	o.ContentType = h.Get("Content-Type")
	o.LastMod = h.Get("Last-Modified")
	o.ETag = strings.Trim(h.Get("ETag"), `"`)
	o.Indexes = map[string][]string{}
	o.Meta = map[string]string{}
	for name, values := range h {
		lower := strings.ToLower(name)
		switch {
		case strings.HasPrefix(lower, indexPrefix):
			idx := lower[len(indexPrefix):]
			for _, v := range values {
				// multi-value 2i arrives comma-joined in a single header line
				for _, part := range strings.Split(v, ",") {
					if part = strings.TrimSpace(part); part != "" {
						o.Indexes[idx] = append(o.Indexes[idx], part)
					}
				}
			}
		case strings.HasPrefix(lower, metaPrefix):
			o.Meta[lower[len(metaPrefix):]] = strings.Join(values, ", ")
		}
	}
}

// writeObjectHeaders sets everything a safe PUT must carry.
func (o *RiakObject) writeObjectHeaders(h http.Header) {
	ct := o.ContentType
	if ct == "" {
		ct = "application/json"
	}
	h.Set("Content-Type", ct)
	if o.Vclock != "" {
		h.Set("X-Riak-Vclock", o.Vclock)
	}
	for idx, values := range o.Indexes {
		for _, v := range values {
			h.Add("X-Riak-Index-"+idx, v)
		}
	}
	for name, v := range o.Meta {
		h.Set("X-Riak-Meta-"+name, v)
	}
}

// IsJSON reports whether the value should be offered to the JSON tree editor.
func (o *RiakObject) IsJSON() bool {
	ct := o.ContentType
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	ct = strings.TrimSpace(strings.ToLower(ct))
	return ct == "application/json" || strings.HasSuffix(ct, "+json") || ct == "text/json"
}
