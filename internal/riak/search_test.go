package riak

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestSearchBuildsQueryAndFiltersBucket(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search/query/users_idx" {
			t.Errorf("path = %q", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("wt") != "json" || q.Get("q") != "plan.name_s:business" || q.Get("rows") != "50" {
			t.Errorf("query = %v", q)
		}
		if fq := q.Get("fq"); fq != `_yz_rt:"default" AND _yz_rb:"users"` {
			t.Errorf("fq = %q", fq)
		}
		fmt.Fprint(w, `{"response":{"numFound":4,"docs":[
			{"_yz_rk":"alice","_yz_rb":"users","_yz_rt":"default"},
			{"_yz_rk":"alice","_yz_rb":"users","_yz_rt":"default"},
			{"_yz_rk":"zed","_yz_rb":"other","_yz_rt":"default"},
			{"_yz_rk":"carol","_yz_rb":"users","_yz_rt":"default"}]}}`)
	}))
	res, err := c.Search(context.Background(), "users_idx", "plan.name_s:business", "", "users", 50)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Keys, []string{"alice", "carol"}) || res.NumFound != 4 {
		t.Fatalf("got %+v", res)
	}
}

func TestSearchErrors(t *testing.T) {
	for status, want := range map[int]string{404: "not found", 400: "invalid search query"} {
		status := status
		c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			fmt.Fprint(w, "org.apache.solr.search.SyntaxError: bad\nmore")
		}))
		_, err := c.Search(context.Background(), "idx", "((", "", "b", 10)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("status %d: err = %v, want it to mention %q", status, err, want)
		}
	}
}
