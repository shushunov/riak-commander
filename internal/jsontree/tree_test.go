package jsontree

import (
	"strings"
	"testing"
)

func mustParse(t *testing.T, s string) *Node {
	t.Helper()
	n, err := Parse([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRoundTripPreservesOrderAndBigInts(t *testing.T) {
	in := `{
  "zeta": 1,
  "alpha": {
    "id": 9007199254740993,
    "nested": [
      1,
      "two",
      true,
      null
    ]
  },
  "beta": "läst \"quoted\"",
  "price": 0.1
}
`
	n := mustParse(t, in)
	out := string(Serialize(n))
	if out != in {
		t.Errorf("round trip mismatch:\n--- in ---\n%s--- out ---\n%s", in, out)
	}
	// key order must be encounter order, not sorted
	keys := []string{}
	for _, c := range n.Children {
		keys = append(keys, c.Key)
	}
	if strings.Join(keys, ",") != "zeta,alpha,beta,price" {
		t.Errorf("key order = %v", keys)
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	for _, bad := range []string{"", "{", `{"a":1}extra`, "nope"} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("Parse(%q) succeeded, want error", bad)
		}
	}
}

func TestSetScalar(t *testing.T) {
	n := mustParse(t, `{"a": 1}`)
	field := n.Children[0]

	if err := SetScalar(field, String, "hello"); err != nil {
		t.Fatal(err)
	}
	if got := string(Serialize(n)); !strings.Contains(got, `"a": "hello"`) {
		t.Errorf("got %s", got)
	}
	if err := SetScalar(field, Number, "3.14"); err != nil {
		t.Fatal(err)
	}
	if err := SetScalar(field, Number, "abc"); err == nil {
		t.Error("invalid number accepted")
	}
	if err := SetScalar(field, Bool, "true"); err != nil {
		t.Fatal(err)
	}
	if err := SetScalar(field, Bool, "yes"); err == nil {
		t.Error("invalid bool accepted")
	}
	if err := SetScalar(field, Null, ""); err != nil {
		t.Fatal(err)
	}
	if got := string(Serialize(n)); !strings.Contains(got, `"a": null`) {
		t.Errorf("got %s", got)
	}
}

func TestSetRawReplacesSubtree(t *testing.T) {
	n := mustParse(t, `{"a": 1}`)
	if err := SetRaw(n.Children[0], `{"deep": [1, 2]}`); err != nil {
		t.Fatal(err)
	}
	got := string(Serialize(n))
	if !strings.Contains(got, `"deep"`) {
		t.Errorf("got %s", got)
	}
	// new grandchildren must point at the right parent for Path()/Delete()
	deep := n.Children[0].Children[0]
	if deep.Parent != n.Children[0] {
		t.Error("reparenting broken")
	}
	if err := SetRaw(n.Children[0], `{bad`); err == nil {
		t.Error("invalid raw JSON accepted")
	}
}

func TestDelete(t *testing.T) {
	n := mustParse(t, `{"a": 1, "b": [10, 20, 30]}`)
	arr := n.Children[1]
	if err := Delete(arr.Children[1]); err != nil { // remove 20
		t.Fatal(err)
	}
	got := string(Serialize(n))
	if strings.Contains(got, "20") || !strings.Contains(got, "30") {
		t.Errorf("got %s", got)
	}
	if err := Delete(n); err == nil {
		t.Error("root delete accepted")
	}
}

func TestAddChild(t *testing.T) {
	n := mustParse(t, `{"a": 1}`)
	if err := AddChild(n, "b", &Node{Kind: String, Str: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := AddChild(n, "a", &Node{Kind: Null}); err == nil {
		t.Error("duplicate key accepted")
	}
	if err := AddChild(n.Children[0], "k", &Node{Kind: Null}); err == nil {
		t.Error("adding into scalar accepted")
	}
	arr := mustParse(t, `[1]`)
	if err := AddChild(arr, "ignored", &Node{Kind: Number, Str: "2"}); err != nil {
		t.Fatal(err)
	}
	if got := string(Serialize(arr)); !strings.Contains(got, "2") {
		t.Errorf("got %s", got)
	}
}

func TestRename(t *testing.T) {
	n := mustParse(t, `{"a": 1, "b": 2}`)
	if err := Rename(n.Children[0], "c"); err != nil {
		t.Fatal(err)
	}
	if err := Rename(n.Children[0], "b"); err == nil {
		t.Error("duplicate rename accepted")
	}
	arr := mustParse(t, `[1]`)
	if err := Rename(arr.Children[0], "x"); err == nil {
		t.Error("renaming array element accepted")
	}
}

func TestPath(t *testing.T) {
	n := mustParse(t, `{"items": [{"name": "x"}]}`)
	name := n.Children[0].Children[0].Children[0]
	if got := name.Path(); got != "$.items[0].name" {
		t.Errorf("path = %q", got)
	}
	if got := n.Path(); got != "$" {
		t.Errorf("root path = %q", got)
	}
}
