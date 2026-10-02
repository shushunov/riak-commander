package riak

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
)

// ListKeys streams keys with ?keys=stream — listing is a full-cluster scan,
// so results are consumed incrementally and the request is aborted as soon as
// `limit` keys have been collected (limit <= 0 means unlimited). Returns the
// keys read and whether the listing was truncated by the limit.
func (c *Client) ListKeys(ctx context.Context, btype, bucket string, limit int) (keys []string, truncated bool, err error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel() // aborts the in-flight body read once we return early

	resp, err := c.do(ctx, http.MethodGet, bucketPath(btype, bucket)+"/keys?keys=stream", nil, nil)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, false, httpError(resp, body)
	}

	// The stream is a concatenation of JSON docs: {"keys":[...]}{"keys":[...]}…
	dec := json.NewDecoder(resp.Body)
	for {
		var chunk struct {
			Keys []string `json:"keys"`
		}
		if err := dec.Decode(&chunk); err != nil {
			if err == io.EOF {
				return keys, false, nil
			}
			// A decode error after hitting the limit is just the aborted read.
			if limit > 0 && len(keys) >= limit {
				return keys[:limit], true, nil
			}
			return keys, false, err
		}
		keys = append(keys, chunk.Keys...)
		if limit > 0 && len(keys) >= limit {
			return keys[:limit], true, nil
		}
	}
}
