package riak

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// errStopStream is returned by ListKeysProgress's chunk callback to end the
// stream once the limit is reached.
var errStopStream = errors.New("stop stream")

// StreamKeys reads a bucket's keys with ?keys=stream and hands each streamed
// chunk to onChunk as it arrives, so callers can process keys without
// holding the whole listing. Listing keys is a full-cluster scan in Riak.
// The stream ends at EOF, when ctx is done, or when onChunk returns an error
// (which is then returned). Riak may repeat a key in several chunks.
func (c *Client) StreamKeys(ctx context.Context, btype, bucket string, onChunk func(keys []string) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel() // aborts the in-flight body read once we return early

	resp, err := c.do(ctx, http.MethodGet, bucketPath(btype, bucket)+"/keys?keys=stream", nil, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return httpError(resp, body)
	}

	// The stream is a concatenation of JSON docs: {"keys":[...]}{"keys":[...]}…
	dec := json.NewDecoder(resp.Body)
	for {
		var chunk struct {
			Keys []string `json:"keys"`
		}
		if err := dec.Decode(&chunk); err != nil {
			if err == io.EOF {
				return nil
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		if len(chunk.Keys) == 0 {
			continue
		}
		if err := onChunk(chunk.Keys); err != nil {
			return err
		}
	}
}

// ListKeys streams keys with ?keys=stream — listing is a full-cluster scan,
// so results are consumed incrementally and the request is aborted as soon as
// `limit` keys have been collected (limit <= 0 means unlimited). Returns the
// keys read and whether the listing was truncated by the limit.
func (c *Client) ListKeys(ctx context.Context, btype, bucket string, limit int) (keys []string, truncated bool, err error) {
	return c.ListKeysProgress(ctx, btype, bucket, limit, nil)
}

// ListKeysProgress is ListKeys with a progress callback: onProgress (may be
// nil) receives the number of keys read so far after every streamed chunk.
// It runs on the calling goroutine.
func (c *Client) ListKeysProgress(ctx context.Context, btype, bucket string, limit int, onProgress func(total int)) (keys []string, truncated bool, err error) {
	err = c.StreamKeys(ctx, btype, bucket, func(chunk []string) error {
		keys = append(keys, chunk...)
		if limit > 0 && len(keys) >= limit {
			keys, truncated = keys[:limit], true
		}
		if onProgress != nil {
			onProgress(len(keys))
		}
		if truncated {
			return errStopStream
		}
		return nil
	})
	if errors.Is(err, errStopStream) {
		err = nil
	}
	return keys, truncated, err
}
