package riak

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// SearchResult is the outcome of a Riak Search (Solr / Yokozuna) query.
type SearchResult struct {
	Keys     []string // matching keys in the requested bucket, in result order
	NumFound int      // total hits in the index (may include other buckets)
}

// Search runs a Solr query against a Riak Search index:
// GET /search/query/<index>?wt=json&q=…. Riak Search was deprecated and is
// absent from Riak KV 3.x builds by default (the last release supporting it
// was 2.9.10); it is only usable on buckets whose `search_index` property
// is set.
//
// One index can cover several buckets, so the query is narrowed to btype /
// bucket with a filter query and the returned _yz_rt/_yz_rb fields are
// checked as well (the filter is passed through to Solr, which is not
// documented by Riak; the client-side check keeps results correct anyway).
func (c *Client) Search(ctx context.Context, index, query, btype, bucket string, rows int) (*SearchResult, error) {
	if btype == "" {
		btype = "default"
	}
	q := url.Values{}
	q.Set("wt", "json")
	q.Set("q", query)
	q.Set("fq", fmt.Sprintf(`_yz_rt:%s AND _yz_rb:%s`, solrQuote(btype), solrQuote(bucket)))
	q.Set("fl", "_yz_rk,_yz_rb,_yz_rt")
	if rows > 0 {
		q.Set("rows", strconv.Itoa(rows))
	}
	resp, err := c.do(ctx, http.MethodGet, "/search/query/"+url.PathEscape(index)+"?"+q.Encode(), nil, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, fmt.Errorf("search index %q not found (is Riak Search enabled on this cluster?)", index)
	case http.StatusBadRequest:
		return nil, fmt.Errorf("invalid search query: %s", strings.TrimSpace(firstLine(body)))
	default:
		return nil, httpError(resp, body)
	}
	var out struct {
		Response struct {
			NumFound int `json:"numFound"`
			Docs     []struct {
				Key    string `json:"_yz_rk"`
				Bucket string `json:"_yz_rb"`
				Type   string `json:"_yz_rt"`
			} `json:"docs"`
		} `json:"response"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parsing search result: %w", err)
	}
	res := &SearchResult{NumFound: out.Response.NumFound}
	seen := map[string]bool{}
	for _, d := range out.Response.Docs {
		if d.Key == "" || (d.Bucket != "" && d.Bucket != bucket) || (d.Type != "" && d.Type != btype) {
			continue
		}
		if !seen[d.Key] { // one key per sibling may come back
			seen[d.Key] = true
			res.Keys = append(res.Keys, d.Key)
		}
	}
	return res, nil
}

// solrQuote quotes a term for a Solr query.
func solrQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func firstLine(b []byte) string {
	s := string(b)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}
