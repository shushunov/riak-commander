package jsontree

import (
	"reflect"
	"testing"
)

const findDoc = `{
  "email": "Alice@Example.com",
  "plan": {"name": "business", "seats": 12},
  "id": 9007199254740993,
  "items": [{"sku": "A-1", "qty": 2}, {"sku": "B-2", "qty": 1}],
  "odd.key": {"x": true},
  "note": null
}`

func values(ms []Match) []string {
	out := []string{}
	for _, m := range ms {
		if m.Name {
			out = append(out, "name:"+m.Node.Key)
		} else {
			out = append(out, m.Node.Path()+"="+m.Node.ScalarDisplay())
		}
	}
	return out
}

func find(t *testing.T, path string, mode MatchMode, value string) []string {
	t.Helper()
	root := mustParse(t, findDoc)
	p, err := ParsePath(path)
	if err != nil {
		t.Fatalf("ParsePath(%q): %v", path, err)
	}
	m, err := NewMatcher(mode, value)
	if err != nil {
		t.Fatalf("NewMatcher: %v", err)
	}
	return values(Find(root, p, m))
}

func TestFindByPath(t *testing.T) {
	cases := []struct {
		path  string
		mode  MatchMode
		value string
		want  []string
	}{
		{"plan.name", MatchEquals, "business", []string{"$.plan.name=business"}},
		{"$.plan.name", MatchEquals, "business", []string{"$.plan.name=business"}},
		{"plan.name", MatchEquals, "Business", []string{}},
		{"email", MatchContains, "example", []string{"$.email=Alice@Example.com"}},
		{"plan.seats", MatchEquals, "12.0", []string{"$.plan.seats=12"}},
		{"plan.seats", MatchEquals, "1.2e1", []string{"$.plan.seats=12"}},
		{"id", MatchEquals, "9007199254740993", []string{"$.id=9007199254740993"}},
		{"id", MatchEquals, "9007199254740992", []string{}},
		{"items[1].sku", MatchEquals, "B-2", []string{"$.items[1].sku=B-2"}},
		{"items[*].sku", MatchRegex, "^[AB]-", []string{"$.items[0].sku=A-1", "$.items[1].sku=B-2"}},
		{"items[5].sku", MatchExists, "", []string{}},
		{`["odd.key"].x`, MatchEquals, "true", []string{`$.odd.key.x=true`}},
		{"note", MatchEquals, "null", []string{"$.note=null"}},
		{"plan", MatchExists, "", []string{"$.plan="}},
		{"plan", MatchEquals, "business", []string{}}, // containers never match values
		{"missing.field", MatchExists, "", []string{}},
	}
	for _, c := range cases {
		got := find(t, c.path, c.mode, c.value)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("Find(%q, %v, %q) = %v, want %v", c.path, c.mode, c.value, got, c.want)
		}
	}
}

func TestFindAnywhere(t *testing.T) {
	if got, want := find(t, "", MatchContains, "sku"), []string{"name:sku", "name:sku"}; !reflect.DeepEqual(got, want) {
		t.Errorf("names: %v, want %v", got, want)
	}
	if got, want := find(t, "", MatchEquals, "B-2"), []string{"$.items[1].sku=B-2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("values: %v, want %v", got, want)
	}
	if got, want := find(t, "", MatchExists, "qty"), []string{"name:qty", "name:qty"}; !reflect.DeepEqual(got, want) {
		t.Errorf("exists: %v, want %v", got, want)
	}
}

func TestParsePathErrors(t *testing.T) {
	for _, p := range []string{"items[", "items[x]", "items[-1]", "a..b", `["unterminated]`} {
		if _, err := ParsePath(p); err == nil {
			t.Errorf("ParsePath(%q): expected an error", p)
		}
	}
	if _, err := NewMatcher(MatchRegex, "(["); err == nil {
		t.Error("expected a regex error")
	}
}
