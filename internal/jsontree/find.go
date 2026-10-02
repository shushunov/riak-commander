package jsontree

import (
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
)

// segment is one step of a field path: an object member, an array index or
// an array wildcard.
type segment struct {
	name  string
	index int // >= 0 for [n]
	kind  segKind
}

type segKind int

const (
	segName segKind = iota
	segIndex
	segAll // [*]
)

// FieldPath is a parsed field path such as plan.items[*].sku. The empty path
// means "anywhere in the document".
type FieldPath struct {
	segs []segment
	src  string
}

// ParsePath parses a field path. Accepted forms:
//
//	plan.name            nested members
//	items[0].sku         array index
//	items[*].sku         every array element
//	["odd.key"].x        quoted member names (for dots, brackets, spaces)
//
// A leading "$" or "$." (as shown in the editor) is accepted and ignored.
func ParsePath(s string) (FieldPath, error) {
	p := FieldPath{src: strings.TrimSpace(s)}
	rest := strings.TrimPrefix(strings.TrimPrefix(p.src, "$"), ".")
	for rest != "" {
		switch {
		case strings.HasPrefix(rest, "["):
			end := strings.Index(rest, "]")
			if end < 0 {
				return p, fmt.Errorf("unclosed [ in path %q", s)
			}
			inner := rest[1:end]
			switch {
			case inner == "*":
				p.segs = append(p.segs, segment{kind: segAll})
			case strings.HasPrefix(inner, `"`):
				name, err := strconv.Unquote(inner)
				if err != nil {
					return p, fmt.Errorf("bad quoted name %s in path %q", inner, s)
				}
				p.segs = append(p.segs, segment{kind: segName, name: name})
			default:
				n, err := strconv.Atoi(inner)
				if err != nil || n < 0 {
					return p, fmt.Errorf("bad array index [%s] in path %q (use [0], [*] or [\"name\"])", inner, s)
				}
				p.segs = append(p.segs, segment{kind: segIndex, index: n})
			}
			rest = strings.TrimPrefix(rest[end+1:], ".")
		default:
			end := strings.IndexAny(rest, ".[")
			if end < 0 {
				end = len(rest)
			}
			if end == 0 {
				return p, fmt.Errorf("empty field name in path %q", s)
			}
			p.segs = append(p.segs, segment{kind: segName, name: rest[:end]})
			rest = strings.TrimPrefix(rest[end:], ".")
		}
	}
	return p, nil
}

// IsEmpty reports whether the path matches anywhere in the document.
func (p FieldPath) IsEmpty() bool { return len(p.segs) == 0 }

func (p FieldPath) String() string { return p.src }

// Resolve returns the nodes the path points to (several with [*]).
func (p FieldPath) Resolve(root *Node) []*Node {
	cur := []*Node{root}
	for _, s := range p.segs {
		var next []*Node
		for _, n := range cur {
			switch {
			case s.kind == segName && n.Kind == Object:
				for _, c := range n.Children {
					if c.Key == s.name {
						next = append(next, c)
					}
				}
			case s.kind == segIndex && n.Kind == Array:
				if s.index < len(n.Children) {
					next = append(next, n.Children[s.index])
				}
			case s.kind == segAll && n.Kind == Array:
				next = append(next, n.Children...)
			}
		}
		cur = next
	}
	return cur
}

// MatchMode selects how values are compared.
type MatchMode int

const (
	MatchEquals   MatchMode = iota // scalar text equal; numbers also numerically
	MatchContains                  // case-insensitive substring
	MatchRegex                     // Go regular expression
	MatchExists                    // the path resolves (value ignored)
)

// Matcher tests nodes against a mode and value.
type Matcher struct {
	mode  MatchMode
	value string
	lower string
	num   *big.Rat
	re    *regexp.Regexp
}

// NewMatcher prepares a matcher; regex syntax errors are reported here.
func NewMatcher(mode MatchMode, value string) (*Matcher, error) {
	m := &Matcher{mode: mode, value: value, lower: strings.ToLower(value)}
	switch mode {
	case MatchRegex:
		re, err := regexp.Compile(value)
		if err != nil {
			return nil, fmt.Errorf("invalid regular expression: %v", err)
		}
		m.re = re
	case MatchEquals:
		if r, ok := new(big.Rat).SetString(strings.TrimSpace(value)); ok {
			m.num = r
		}
	}
	return m, nil
}

// matchText tests one string (a scalar value or a member name).
func (m *Matcher) matchText(s string) bool {
	switch m.mode {
	case MatchEquals:
		return s == m.value
	case MatchContains:
		return strings.Contains(strings.ToLower(s), m.lower)
	case MatchRegex:
		return m.re.MatchString(s)
	}
	return false
}

// matchValue tests a resolved node. Containers never match a value test
// (only MatchExists accepts them).
func (m *Matcher) matchValue(n *Node) bool {
	if m.mode == MatchExists {
		return true
	}
	if n.Kind == Object || n.Kind == Array {
		return false
	}
	if m.mode == MatchEquals && n.Kind == Number && m.num != nil {
		// exact rational comparison: 12 == 12.0 == 1.2e1, and 64-bit IDs
		// are never rounded
		if r, ok := new(big.Rat).SetString(n.Str); ok && r.Cmp(m.num) == 0 {
			return true
		}
	}
	return m.matchText(n.ScalarDisplay())
}

// Match is one hit: the node and where it is.
type Match struct {
	Node *Node
	// Name is set when an object member's name (not its value) matched,
	// which only happens for path-less searches.
	Name bool
}

// Find returns the matches of m in the document. With a path, the nodes the
// path resolves to are tested. Without one, every member name and every
// scalar value in the document is tested (MatchExists then matches member
// names equal to the value).
func Find(root *Node, path FieldPath, m *Matcher) []Match {
	var out []Match
	if !path.IsEmpty() {
		for _, n := range path.Resolve(root) {
			if m.matchValue(n) {
				out = append(out, Match{Node: n})
			}
		}
		return out
	}
	var walk func(n *Node)
	walk = func(n *Node) {
		if n.Parent != nil && n.Parent.Kind == Object {
			nameHit := false
			if m.mode == MatchExists {
				nameHit = n.Key == m.value
			} else {
				nameHit = m.matchText(n.Key)
			}
			if nameHit {
				out = append(out, Match{Node: n, Name: true})
			}
		}
		if n.Kind == Object || n.Kind == Array {
			for _, c := range n.Children {
				walk(c)
			}
			return
		}
		if m.mode != MatchExists && m.matchValue(n) {
			out = append(out, Match{Node: n})
		}
	}
	walk(root)
	return out
}
