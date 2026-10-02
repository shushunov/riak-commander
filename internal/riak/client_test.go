package riak

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newTestClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c, err := NewClient(srv.URL, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestGetObjectParsesMetadata(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/buckets/users/keys/alice" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		h := w.Header()
		h.Set("Content-Type", "application/json; charset=utf-8")
		h.Set("X-Riak-Vclock", "a85hYGBgzGDKBVIcR4M2cgczH7HPYEpkzGNlsP/VfIYvCwA=")
		h.Set("x-riak-index-email_bin", "alice@example.com")
		h.Add("X-Riak-Index-Roles_bin", "admin, editor") // multi-value comma form
		h.Set("X-Riak-Meta-Origin", "test")
		h.Set("Last-Modified", "Wed, 01 Jan 2025 00:00:00 GMT")
		h.Set("ETag", `"abc123"`)
		fmt.Fprint(w, `{"email":"alice@example.com"}`)
	}))

	obj, err := c.GetObject(context.Background(), "", "users", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if obj.Vclock == "" {
		t.Error("vclock not captured")
	}
	if !obj.IsJSON() {
		t.Errorf("IsJSON() = false for %q", obj.ContentType)
	}
	want := map[string][]string{
		"email_bin": {"alice@example.com"},
		"roles_bin": {"admin", "editor"},
	}
	if !reflect.DeepEqual(obj.Indexes, want) {
		t.Errorf("indexes = %v, want %v", obj.Indexes, want)
	}
	if obj.Meta["origin"] != "test" {
		t.Errorf("meta = %v", obj.Meta)
	}
	if obj.ETag != "abc123" {
		t.Errorf("etag = %q", obj.ETag)
	}
}

func TestGetObjectNotFound(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	_, err := c.GetObject(context.Background(), "", "users", "ghost")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestGetObjectSiblings(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Riak-Vclock", "vc300")
		w.WriteHeader(http.StatusMultipleChoices)
		fmt.Fprint(w, "Siblings:\n16vic4eU9ny46o4KPiDz1f\n4v5xOg4bVwUYZdMkqf0d6I\n")
	}))
	_, err := c.GetObject(context.Background(), "", "webhooks", "w1")
	var sib *ErrSiblings
	if !errors.As(err, &sib) {
		t.Fatalf("err = %v, want ErrSiblings", err)
	}
	if len(sib.Vtags) != 2 || sib.Vclock != "vc300" {
		t.Errorf("siblings = %+v", sib)
	}
}

// The guard for the two critical safety constraints: a PUT of an existing
// object must resend the vclock, every 2i index, meta and content type.
func TestPutObjectRoundTripsVclockAndIndexes(t *testing.T) {
	var got *http.Request
	var gotIdx map[string][]string
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		gotIdx = map[string][]string{}
		for name, vals := range r.Header {
			lower := strings.ToLower(name)
			if strings.HasPrefix(lower, "x-riak-index-") {
				vs := append([]string(nil), vals...)
				sort.Strings(vs)
				gotIdx[lower] = vs
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	obj := &RiakObject{
		Bucket:      "users",
		Key:         "alice",
		Body:        []byte(`{"email":"new@example.com"}`),
		ContentType: "application/json",
		Vclock:      "vclock-token",
		Indexes: map[string][]string{
			"email_bin": {"new@example.com"},
			"roles_bin": {"admin", "editor"},
		},
		Meta: map[string]string{"origin": "test"},
	}
	if err := c.PutObject(context.Background(), obj, false); err != nil {
		t.Fatal(err)
	}
	if got.Header.Get("X-Riak-Vclock") != "vclock-token" {
		t.Error("vclock not sent on PUT")
	}
	if got.Header.Get("Content-Type") != "application/json" {
		t.Error("content-type not sent")
	}
	if got.Header.Get("X-Riak-Meta-origin") != "test" {
		t.Error("meta not sent")
	}
	wantIdx := map[string][]string{
		"x-riak-index-email_bin": {"new@example.com"},
		"x-riak-index-roles_bin": {"admin", "editor"},
	}
	if !reflect.DeepEqual(gotIdx, wantIdx) {
		t.Errorf("PUT indexes = %v, want %v — 2i entries would be destroyed", gotIdx, wantIdx)
	}
	if got.URL.RawQuery != "returnbody=false" {
		t.Errorf("query = %q", got.URL.RawQuery)
	}
}

func TestPutObjectIfNoneMatch(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") != "*" {
			t.Error("If-None-Match not sent")
		}
		w.WriteHeader(http.StatusPreconditionFailed)
	}))
	err := c.PutObject(context.Background(), &RiakObject{Bucket: "b", Key: "k", Body: []byte("{}")}, true)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("err = %v, want already-exists", err)
	}
}

func TestTypedAndEscapedURLs(t *testing.T) {
	var paths []string
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.EscapedPath())
		switch {
		case strings.HasSuffix(r.URL.Path, "/buckets"):
			fmt.Fprint(w, `{"buckets":[]}`)
		default:
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{}`)
		}
	}))

	ctx := context.Background()
	if _, err := c.ListBuckets(ctx, "sessions"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListBuckets(ctx, "default"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetObject(ctx, "files", "uploads", "/dir/some file.jpg"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"/types/sessions/buckets",
		"/buckets",
		"/types/files/buckets/uploads/keys/%2Fdir%2Fsome%20file.jpg",
	}
	for i, w := range want {
		if !strings.HasPrefix(paths[i], w) {
			t.Errorf("path[%d] = %q, want prefix %q", i, paths[i], w)
		}
	}
}

func TestListKeysStreamTruncates(t *testing.T) {
	var chunksSent atomic.Int64 // written by the handler goroutine
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "keys=stream" {
			t.Errorf("query = %q", r.URL.RawQuery)
		}
		fl := w.(http.Flusher)
		// endless stream: the client must stop reading at its cap
		for i := 0; ; i++ {
			chunksSent.Add(1)
			if _, err := fmt.Fprintf(w, `{"keys":["k%d-1","k%d-2","k%d-3"]}`, i, i, i); err != nil {
				return
			}
			fl.Flush()
		}
	}))

	keys, truncated, err := c.ListKeys(context.Background(), "", "customers", 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 7 || !truncated {
		t.Fatalf("got %d keys truncated=%v, want 7/true", len(keys), truncated)
	}
	if n := chunksSent.Load(); n > 100 {
		t.Errorf("server sent %d chunks — client did not abort the stream", n)
	}
}

func TestListKeysProgressReportsRunningTotal(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"keys":["a","b"]}{"keys":[]}{"keys":["c","d","e"]}`)
	}))
	var got []int
	keys, truncated, err := c.ListKeysProgress(context.Background(), "", "customers", 4, func(n int) {
		got = append(got, n)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 4 || !truncated {
		t.Fatalf("got %d keys truncated=%v, want 4/true", len(keys), truncated)
	}
	if want := []int{2, 2, 4}; !reflect.DeepEqual(got, want) {
		t.Fatalf("progress = %v, want %v (capped at the limit)", got, want)
	}
}

func TestListKeysStreamComplete(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"keys":["a","b"]}{"keys":[]}{"keys":["c"]}`)
	}))
	keys, truncated, err := c.ListKeys(context.Background(), "", "customers", 1000)
	if err != nil {
		t.Fatal(err)
	}
	if truncated || !reflect.DeepEqual(keys, []string{"a", "b", "c"}) {
		t.Fatalf("keys = %v truncated=%v", keys, truncated)
	}
}

func TestIndexQuery(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wantPath := "/buckets/users/index/email_bin/alice@example.com"
		if r.URL.EscapedPath() != wantPath {
			t.Errorf("path = %q, want %q", r.URL.EscapedPath(), wantPath)
		}
		if r.URL.Query().Get("max_results") != "100" {
			t.Errorf("max_results = %q", r.URL.Query().Get("max_results"))
		}
		fmt.Fprint(w, `{"keys":["alice"],"continuation":"g20"}`)
	}))
	res, err := c.IndexQuery(context.Background(), "", "users", "email_bin", "alice@example.com", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Keys) != 1 || res.Continuation != "g20" {
		t.Errorf("res = %+v", res)
	}
}

func TestIndexQueryRange(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		want := "/types/jobs/buckets/queue/index/owner_bin/a/z"
		if r.URL.EscapedPath() != want {
			t.Errorf("path = %q, want %q", r.URL.EscapedPath(), want)
		}
		fmt.Fprint(w, `{"keys":["k1","k2"]}`)
	}))
	res, err := c.IndexQuery(context.Background(), "jobs", "queue", "owner_bin", "a", "z", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Keys) != 2 {
		t.Errorf("res = %+v", res)
	}
}

func TestBucketPropsAndDatatype(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/props"):
			fmt.Fprint(w, `{"props":{"name":"stats","datatype":"map","allow_mult":true}}`)
		case strings.Contains(r.URL.Path, "/datatypes/"):
			if r.URL.Path != "/types/stats/buckets/daily/datatypes/day1" {
				t.Errorf("datatype path = %q", r.URL.Path)
			}
			fmt.Fprint(w, `{"type":"map","value":{"hits_counter":42}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	props, err := c.BucketProps(context.Background(), "stats", "daily")
	if err != nil {
		t.Fatal(err)
	}
	if props["datatype"] != "map" {
		t.Errorf("props = %v", props)
	}
	body, err := c.GetDatatype(context.Background(), "stats", "daily", "day1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "hits_counter") {
		t.Errorf("datatype body = %s", body)
	}
}

func TestErrorIncludesStatusAndBody(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "insufficient vnodes available", http.StatusServiceUnavailable)
	}))
	_, err := c.ListBuckets(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "503") || !strings.Contains(err.Error(), "insufficient vnodes") {
		t.Fatalf("err = %v", err)
	}
}

func TestPing(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ping" {
			t.Errorf("path = %q", r.URL.Path)
		}
		fmt.Fprint(w, "OK")
	}))
	if err := c.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// Guard against regressions in the URL builder itself.
func TestBucketPath(t *testing.T) {
	cases := []struct{ btype, bucket, want string }{
		{"", "users", "/buckets/users"},
		{"default", "users", "/buckets/users"},
		{"sessions", "tokens", "/types/sessions/buckets/tokens"},
		{"blobs", "a b/c", "/types/blobs/buckets/a%20b%2Fc"},
	}
	for _, tc := range cases {
		if got := bucketPath(tc.btype, tc.bucket); got != tc.want {
			t.Errorf("bucketPath(%q,%q) = %q, want %q", tc.btype, tc.bucket, got, tc.want)
		}
	}
	if _, err := url.Parse(bucketPath("t", "b")); err != nil {
		t.Error(err)
	}
}
