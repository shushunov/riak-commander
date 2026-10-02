package jsontree

import (
	"encoding/json"
	"fmt"
)

// SetScalar replaces a node's value in place. kind must be a scalar kind;
// literal is the user-typed text (for Number it is validated as a JSON
// number token; for Bool it must be true/false).
func SetScalar(n *Node, kind Kind, literal string) error {
	switch kind {
	case String:
		n.Kind, n.Str = String, literal
	case Number:
		var num json.Number
		if err := json.Unmarshal([]byte(literal), &num); err != nil {
			return fmt.Errorf("%q is not a valid JSON number", literal)
		}
		n.Kind, n.Str = Number, num.String()
	case Bool:
		switch literal {
		case "true":
			n.Kind, n.BoolVal = Bool, true
		case "false":
			n.Kind, n.BoolVal = Bool, false
		default:
			return fmt.Errorf("boolean must be true or false, got %q", literal)
		}
		n.Str = ""
	case Null:
		n.Kind, n.Str = Null, ""
	default:
		return fmt.Errorf("cannot set container kind %s as scalar", kind)
	}
	if kind != Bool {
		n.BoolVal = false
	}
	n.Children = nil
	return nil
}

// SetRaw replaces a node's value with an arbitrary parsed JSON fragment
// (used by the "raw JSON" edit type to paste sub-objects/arrays).
func SetRaw(n *Node, jsonText string) error {
	parsed, err := Parse([]byte(jsonText))
	if err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	n.Kind = parsed.Kind
	n.Str = parsed.Str
	n.BoolVal = parsed.BoolVal
	n.Children = parsed.Children
	for _, c := range n.Children {
		c.Parent = n
	}
	return nil
}

// Delete removes n from its parent. The root cannot be deleted.
func Delete(n *Node) error {
	if n.Parent == nil {
		return fmt.Errorf("cannot delete the root value")
	}
	sibs := n.Parent.Children
	for i, sib := range sibs {
		if sib == n {
			n.Parent.Children = append(sibs[:i], sibs[i+1:]...)
			n.Parent = nil
			return nil
		}
	}
	return fmt.Errorf("node not found in parent")
}

// AddChild appends a new member/element to a container. For objects the key
// must be non-duplicate; for arrays key is ignored.
func AddChild(parent *Node, key string, child *Node) error {
	switch parent.Kind {
	case Object:
		for _, c := range parent.Children {
			if c.Key == key {
				return fmt.Errorf("key %q already exists", key)
			}
		}
		child.Key = key
	case Array:
		child.Key = ""
	default:
		return fmt.Errorf("can only add into an object or array")
	}
	child.Parent = parent
	parent.Children = append(parent.Children, child)
	return nil
}

// Rename changes an object member's key, rejecting duplicates.
func Rename(n *Node, newKey string) error {
	if n.Parent == nil || n.Parent.Kind != Object {
		return fmt.Errorf("only object members can be renamed")
	}
	for _, sib := range n.Parent.Children {
		if sib != n && sib.Key == newKey {
			return fmt.Errorf("key %q already exists", newKey)
		}
	}
	n.Key = newKey
	return nil
}
