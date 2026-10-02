package riak

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Client talks to Riak's HTTP interface (port 8098 by default).
type Client struct {
	base    string // canonical base URL, e.g. http://127.0.0.1:8098
	display string // short form for the UI and history, e.g. 127.0.0.1:8098
	http    *http.Client
	timeout time.Duration
}

// NewClient builds a client for an address accepted by NormalizeAddress
// (host, host:port or an http/https URL). No global http.Client timeout is
// set because key listing streams for a long time; per-request deadlines come
// from the contexts callers pass in (see Timeout).
func NewClient(address string, timeout time.Duration) (*Client, error) {
	base, display, err := NormalizeAddress(address)
	if err != nil {
		return nil, err
	}
	return &Client{base: base, display: display, http: &http.Client{}, timeout: timeout}, nil
}

// Timeout is the suggested per-request deadline for non-streaming calls.
func (c *Client) Timeout() time.Duration { return c.timeout }

// Base is the canonical base URL every request path is appended to.
func (c *Client) Base() string { return c.base }

// Address is the short display form of the endpoint; it is also the key the
// server history is stored under.
func (c *Client) Address() string { return c.display }

// ErrSiblings is returned by GetObject when the object has unresolved
// siblings (HTTP 300). Vclock is the 300-response vclock: a PUT carrying it
// resolves the siblings.
type ErrSiblings struct {
	Vtags  []string
	Vclock string
}

func (e *ErrSiblings) Error() string {
	return fmt.Sprintf("object has %d siblings", len(e.Vtags))
}

// ErrNotFound is returned when the key does not exist.
var ErrNotFound = fmt.Errorf("not found")

// httpError carries the status and a snippet of the response body.
func httpError(resp *http.Response, body []byte) error {
	snippet := strings.TrimSpace(string(body))
	if len(snippet) > 200 {
		snippet = snippet[:200] + "…"
	}
	if snippet != "" {
		return fmt.Errorf("riak: %s: %s", resp.Status, snippet)
	}
	return fmt.Errorf("riak: %s", resp.Status)
}

// bucketPath builds the URL path prefix for a (type, bucket) pair. The
// default type uses the plain /buckets form (the legacy, untyped URL scheme
// every Riak version understands); any other type uses /types/<t>/buckets.
func bucketPath(btype, bucket string) string {
	if btype == "" || btype == "default" {
		return "/buckets/" + url.PathEscape(bucket)
	}
	return "/types/" + url.PathEscape(btype) + "/buckets/" + url.PathEscape(bucket)
}

func typePath(btype string) string {
	if btype == "" || btype == "default" {
		return ""
	}
	return "/types/" + url.PathEscape(btype)
}

func (c *Client) do(ctx context.Context, method, path string, body io.Reader, hdr http.Header) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return nil, err
	}
	for k, vs := range hdr {
		req.Header[k] = vs
	}
	return c.http.Do(req)
}

// Ping checks connectivity: GET /ping.
func (c *Client) Ping(ctx context.Context) error {
	resp, err := c.do(ctx, http.MethodGet, "/ping", nil, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return httpError(resp, body)
	}
	return nil
}

// ListBuckets enumerates buckets under a type. Riak only returns non-empty
// buckets, and this is an expensive full-scan operation on large clusters.
func (c *Client) ListBuckets(ctx context.Context, btype string) ([]string, error) {
	resp, err := c.do(ctx, http.MethodGet, typePath(btype)+"/buckets?buckets=true", nil, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, httpError(resp, body)
	}
	var out struct {
		Buckets []string `json:"buckets"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parsing bucket list: %w", err)
	}
	sort.Strings(out.Buckets)
	return out.Buckets, nil
}

// GetObject fetches a key with all write-critical metadata.
func (c *Client) GetObject(ctx context.Context, btype, bucket, key string) (*RiakObject, error) {
	resp, err := c.do(ctx, http.MethodGet, bucketPath(btype, bucket)+"/keys/"+url.PathEscape(key), nil, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	switch resp.StatusCode {
	case http.StatusOK:
		obj := &RiakObject{BucketType: btype, Bucket: bucket, Key: key, Body: body}
		obj.parseObjectHeaders(resp.Header)
		return obj, nil
	case http.StatusMultipleChoices:
		vtags := []string{}
		for _, line := range strings.Split(string(body), "\n") {
			line = strings.TrimSpace(line)
			if line != "" && line != "Siblings:" {
				vtags = append(vtags, line)
			}
		}
		return nil, &ErrSiblings{Vtags: vtags, Vclock: resp.Header.Get("X-Riak-Vclock")}
	case http.StatusNotFound:
		return nil, ErrNotFound
	default:
		return nil, httpError(resp, body)
	}
}

// GetSibling fetches one sibling of a conflicted object by vtag.
func (c *Client) GetSibling(ctx context.Context, btype, bucket, key, vtag string) (*RiakObject, error) {
	path := bucketPath(btype, bucket) + "/keys/" + url.PathEscape(key) + "?vtag=" + url.QueryEscape(vtag)
	resp, err := c.do(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, httpError(resp, body)
	}
	obj := &RiakObject{BucketType: btype, Bucket: bucket, Key: key, Body: body}
	obj.parseObjectHeaders(resp.Header)
	return obj, nil
}

// PutObject writes an object, round-tripping vclock, 2i indexes and meta.
// ifNoneMatch guards creation of a brand-new key against clobbering.
func (c *Client) PutObject(ctx context.Context, obj *RiakObject, ifNoneMatch bool) error {
	hdr := http.Header{}
	obj.writeObjectHeaders(hdr)
	if ifNoneMatch {
		hdr.Set("If-None-Match", "*")
	}
	path := bucketPath(obj.BucketType, obj.Bucket) + "/keys/" + url.PathEscape(obj.Key) + "?returnbody=false"
	resp, err := c.do(ctx, http.MethodPut, path, bytes.NewReader(obj.Body), hdr)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	switch resp.StatusCode {
	case http.StatusOK, http.StatusNoContent, http.StatusCreated:
		return nil
	case http.StatusPreconditionFailed:
		return fmt.Errorf("key already exists")
	default:
		return httpError(resp, body)
	}
}

// DeleteObject removes a key; vclock (if known) is sent for causal safety.
func (c *Client) DeleteObject(ctx context.Context, btype, bucket, key, vclock string) error {
	hdr := http.Header{}
	if vclock != "" {
		hdr.Set("X-Riak-Vclock", vclock)
	}
	resp, err := c.do(ctx, http.MethodDelete, bucketPath(btype, bucket)+"/keys/"+url.PathEscape(key), nil, hdr)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	switch resp.StatusCode {
	case http.StatusNoContent, http.StatusOK, http.StatusNotFound:
		return nil // 404 on delete is success for our purposes
	default:
		return httpError(resp, body)
	}
}

// GetDatatype reads a CRDT value (maps/sets/counters). These live behind a
// different endpoint and cannot be edited through the KV API — read-only.
func (c *Client) GetDatatype(ctx context.Context, btype, bucket, key string) ([]byte, error) {
	path := "/types/" + url.PathEscape(btype) + "/buckets/" + url.PathEscape(bucket) + "/datatypes/" + url.PathEscape(key)
	resp, err := c.do(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	switch resp.StatusCode {
	case http.StatusOK:
		return body, nil
	case http.StatusNotFound:
		return nil, ErrNotFound
	default:
		return nil, httpError(resp, body)
	}
}

// IndexResult is the outcome of a 2i query.
type IndexResult struct {
	Keys         []string
	Continuation string
}

// IndexQuery runs an exact-match (to == "") or range 2i query.
func (c *Client) IndexQuery(ctx context.Context, btype, bucket, index, from, to string, maxResults int) (*IndexResult, error) {
	path := bucketPath(btype, bucket) + "/index/" + url.PathEscape(index) + "/" + url.PathEscape(from)
	if to != "" {
		path += "/" + url.PathEscape(to)
	}
	if maxResults > 0 {
		path += "?max_results=" + strconv.Itoa(maxResults)
	}
	resp, err := c.do(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, httpError(resp, body)
	}
	var out struct {
		Keys         []string `json:"keys"`
		Continuation string   `json:"continuation"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parsing 2i result: %w", err)
	}
	return &IndexResult{Keys: out.Keys, Continuation: out.Continuation}, nil
}

// BucketProps fetches bucket properties as a raw JSON map. Used both for the
// props modal and to detect the `datatype` property (CRDT buckets).
func (c *Client) BucketProps(ctx context.Context, btype, bucket string) (map[string]any, error) {
	resp, err := c.do(ctx, http.MethodGet, bucketPath(btype, bucket)+"/props", nil, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, httpError(resp, body)
	}
	var out struct {
		Props map[string]any `json:"props"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parsing bucket props: %w", err)
	}
	return out.Props, nil
}
