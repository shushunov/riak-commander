// Package jsontree is an order-preserving JSON document model for the tree
// editor. encoding/json's map[string]any loses object key order and mangles
// large integers through float64; this model keeps keys in encounter order
// and scalars as raw tokens, so a parse→serialize round-trip is faithful.
package jsontree

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

type Kind int

const (
	Object Kind = iota
	Array
	String
	Number
	Bool
	Null
)

func (k Kind) String() string {
	switch k {
	case Object:
		return "object"
	case Array:
		return "array"
	case String:
		return "string"
	case Number:
		return "number"
	case Bool:
		return "bool"
	case Null:
		return "null"
	}
	return "?"
}

type Node struct {
	Kind     Kind
	Key      string // member name in parent object; unused for array elements
	Children []*Node
	Str      string // String: decoded value; Number: raw token (never float64)
	BoolVal  bool
	Parent   *Node
}

// Parse builds a tree from JSON bytes.
func Parse(data []byte) (*Node, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	root, err := parseValue(dec)
	if err != nil {
		return nil, err
	}
	// reject trailing garbage
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("unexpected data after JSON value")
	}
	return root, nil
}

func parseValue(dec *json.Decoder) (*Node, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	return nodeFromToken(dec, tok)
}

func nodeFromToken(dec *json.Decoder, tok json.Token) (*Node, error) {
	switch v := tok.(type) {
	case json.Delim:
		switch v {
		case '{':
			n := &Node{Kind: Object}
			for dec.More() {
				keyTok, err := dec.Token()
				if err != nil {
					return nil, err
				}
				key, ok := keyTok.(string)
				if !ok {
					return nil, fmt.Errorf("expected object key, got %v", keyTok)
				}
				child, err := parseValue(dec)
				if err != nil {
					return nil, err
				}
				child.Key = key
				child.Parent = n
				n.Children = append(n.Children, child)
			}
			if _, err := dec.Token(); err != nil { // consume '}'
				return nil, err
			}
			return n, nil
		case '[':
			n := &Node{Kind: Array}
			for dec.More() {
				child, err := parseValue(dec)
				if err != nil {
					return nil, err
				}
				child.Parent = n
				n.Children = append(n.Children, child)
			}
			if _, err := dec.Token(); err != nil { // consume ']'
				return nil, err
			}
			return n, nil
		}
		return nil, fmt.Errorf("unexpected delimiter %v", v)
	case string:
		return &Node{Kind: String, Str: v}, nil
	case json.Number:
		return &Node{Kind: Number, Str: v.String()}, nil
	case bool:
		return &Node{Kind: Bool, BoolVal: v}, nil
	case nil:
		return &Node{Kind: Null}, nil
	}
	return nil, fmt.Errorf("unexpected token %v", tok)
}

// Serialize renders the tree back to indented JSON, preserving key order and
// raw number tokens.
func Serialize(n *Node) []byte {
	var b bytes.Buffer
	write(&b, n, 0)
	b.WriteByte('\n')
	return b.Bytes()
}

func write(b *bytes.Buffer, n *Node, depth int) {
	switch n.Kind {
	case Object:
		if len(n.Children) == 0 {
			b.WriteString("{}")
			return
		}
		b.WriteString("{\n")
		for i, c := range n.Children {
			indent(b, depth+1)
			keyBytes, _ := json.Marshal(c.Key)
			b.Write(keyBytes)
			b.WriteString(": ")
			write(b, c, depth+1)
			if i < len(n.Children)-1 {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		indent(b, depth)
		b.WriteByte('}')
	case Array:
		if len(n.Children) == 0 {
			b.WriteString("[]")
			return
		}
		b.WriteString("[\n")
		for i, c := range n.Children {
			indent(b, depth+1)
			write(b, c, depth+1)
			if i < len(n.Children)-1 {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		indent(b, depth)
		b.WriteByte(']')
	case String:
		s, _ := json.Marshal(n.Str)
		b.Write(s)
	case Number:
		b.WriteString(n.Str)
	case Bool:
		b.WriteString(strconv.FormatBool(n.BoolVal))
	case Null:
		b.WriteString("null")
	}
}

func indent(b *bytes.Buffer, depth int) {
	for i := 0; i < depth; i++ {
		b.WriteString("  ")
	}
}

// ScalarDisplay returns the value as shown in tree labels and edit fields.
func (n *Node) ScalarDisplay() string {
	switch n.Kind {
	case String:
		return n.Str
	case Number:
		return n.Str
	case Bool:
		return strconv.FormatBool(n.BoolVal)
	case Null:
		return "null"
	}
	return ""
}

// Path renders a jq-style path for the header line, e.g. $.items[3].name.
func (n *Node) Path() string {
	if n.Parent == nil {
		return "$"
	}
	var parts []string
	for cur := n; cur.Parent != nil; cur = cur.Parent {
		switch cur.Parent.Kind {
		case Object:
			parts = append(parts, "."+cur.Key)
		case Array:
			idx := 0
			for i, sib := range cur.Parent.Children {
				if sib == cur {
					idx = i
					break
				}
			}
			parts = append(parts, "["+strconv.Itoa(idx)+"]")
		}
	}
	var b strings.Builder
	b.WriteByte('$')
	for i := len(parts) - 1; i >= 0; i-- {
		b.WriteString(parts[i])
	}
	return b.String()
}
