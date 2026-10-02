package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/rivo/tview"

	"github.com/shushunov/riak-commander/internal/jsontree"
	"github.com/shushunov/riak-commander/internal/riak"
)

// dirtyGuard runs action immediately when there are no unsaved edits;
// otherwise asks Save / Discard / Cancel first.
func (v *Viewer) dirtyGuard(action func()) {
	if v.cur == nil || !v.cur.dirty {
		action()
		return
	}
	v.app.choice("Unsaved changes",
		fmt.Sprintf("%q has unsaved edits.\n\nSave them to Riak, discard them, or go back?", v.cur.obj.Key),
		[]string{"Save", "Discard", "Cancel"},
		func(label string) {
			switch label {
			case "Save":
				v.app.save(action)
			case "Discard":
				v.discard()
				action()
			}
		})
}

func (v *Viewer) markDirty(jn *jsontree.Node) {
	v.cur.dirty = true
	v.refreshNode(jn)
	v.render()
}

// discard re-parses the original body, dropping all edits.
func (v *Viewer) discard() {
	c := v.cur
	c.dirty = false
	if root, err := jsontree.Parse(c.obj.Body); err == nil {
		c.root = root
	}
	v.render()
}

// save serializes the tree and PUTs it with the stored vclock, 2i indexes and
// meta, the things that must never be dropped. After a successful PUT the
// object is re-fetched so the next save carries a fresh vclock. onDone (may be
// nil) runs after the re-fetch completes.
func (a *App) save(onDone func()) {
	v := a.viewer
	c := v.cur
	if c == nil || !c.editable || c.mode != modeTree {
		a.status.Info("Nothing to save: no editable value is open")
		return
	}
	if !c.dirty && c.sibling == "" {
		a.status.Info("No changes to save")
		if onDone != nil {
			onDone()
		}
		return
	}
	obj := *c.obj // shallow copy; Body replaced below
	obj.Body = jsontree.Serialize(c.root)
	a.async("saving "+obj.Key, 0, func(ctx context.Context) (any, error) {
		if err := a.client.PutObject(ctx, &obj, false); err != nil {
			return nil, err
		}
		// refresh vclock (and pick up whatever the cluster now returns)
		return a.client.GetObject(ctx, obj.BucketType, obj.Bucket, obj.Key)
	}, func(res any, err error) {
		if err != nil {
			var sib *riak.ErrSiblings
			if errors.As(err, &sib) {
				a.siblingDialog(obj.BucketType, obj.Bucket, obj.Key, sib)
				return
			}
			a.alert("Save failed", err.Error())
			return
		}
		v.show(res.(*riak.RiakObject))
		a.status.Success("Saved %s (vclock refreshed, %s preserved)",
			obj.Key, pluralize(len(obj.Indexes), "2i index", "2i indexes"))
		if onDone != nil {
			onDone()
		}
	})
}

func orDefault(btype string) string {
	if btype == "" {
		return "default"
	}
	return btype
}

// ---- field edit dialogs ----

var kindNames = []string{"string", "number", "bool", "null", "raw JSON"}

const valueHint = "Type: string stores the text as-is; number/bool/null expect a literal " +
	"(42, 1.5e3, true, null); raw JSON accepts any JSON, e.g. {\"a\":1} or [1,2]."

func kindFromName(name string) (jsontree.Kind, bool) {
	switch name {
	case "string":
		return jsontree.String, false
	case "number":
		return jsontree.Number, false
	case "bool":
		return jsontree.Bool, false
	case "null":
		return jsontree.Null, false
	}
	return 0, true // raw JSON
}

func guessKindIndex(jn *jsontree.Node) int {
	switch jn.Kind {
	case jsontree.String:
		return 0
	case jsontree.Number:
		return 1
	case jsontree.Bool:
		return 2
	case jsontree.Null:
		return 3
	}
	return 4
}

// applyValue sets n from a kind name and literal.
func applyValue(n *jsontree.Node, kindName, val string) error {
	if kind, raw := kindFromName(kindName); !raw {
		return jsontree.SetScalar(n, kind, val)
	}
	return jsontree.SetRaw(n, val)
}

// editNode opens the value editor for a node (F4/Enter on a scalar; also
// reachable on containers via F4, editing them as raw JSON).
func (a *App) editNode(jn *jsontree.Node) {
	v := a.viewer
	if v.cur == nil || !v.cur.editable {
		a.status.Warn("This value is read-only: %s", readOnlyReason(v.cur))
		return
	}
	initial := jn.ScalarDisplay()
	if jn.Kind == jsontree.Object || jn.Kind == jsontree.Array {
		initial = strings.TrimSpace(string(jsontree.Serialize(jn)))
	}
	a.form(formSpec{
		title: "Edit " + jn.Path(),
		hint:  valueHint + " Changes stay local until you press F2.",
		build: func(f *tview.Form) {
			f.AddDropDown("Type", kindNames, guessKindIndex(jn), nil)
			f.AddInputField("Value", initial, 52, nil, nil)
			f.SetFocus(1)
		},
		onOK: func(f *tview.Form) error {
			if err := applyValue(jn, dropdownValue(f, "Type"), inputText(f, "Value")); err != nil {
				return err
			}
			if jn.Kind == jsontree.Object || jn.Kind == jsontree.Array {
				v.buildTree() // structure changed — rebuild
				if tn, ok := v.cur.nodeMap[jn]; ok {
					v.tree.SetCurrentNode(tn)
				}
			}
			v.markDirty(jn)
			a.status.Info("Edited %s (unsaved: F2 to save)", jn.Path())
			return nil
		},
	})
}

func readOnlyReason(c *loaded) string {
	if c == nil {
		return "no value is open"
	}
	switch c.mode {
	case modeCRDT:
		return "CRDT values can only be changed through Riak's datatype operations"
	case modeHex:
		return "binary values cannot be edited here"
	case modeRaw:
		if len(c.obj.Body) > treeEditLimit {
			return "values over 1 MiB open read-only"
		}
		return "only valid JSON values can be edited"
	}
	return "switch back to the tree view (F3) to edit"
}

// addChild adds a member/element into the selected container (or the parent
// of a selected scalar).
func (a *App) addChild() {
	v := a.viewer
	jn := v.selectedNode()
	if jn == nil || !v.cur.editable {
		a.status.Info("Select a node in the JSON tree first")
		return
	}
	parent := jn
	if parent.Kind != jsontree.Object && parent.Kind != jsontree.Array {
		parent = jn.Parent
	}
	if parent == nil {
		return
	}
	intoObject := parent.Kind == jsontree.Object
	what := "element to array " + parent.Path()
	if intoObject {
		what = "field to object " + parent.Path()
	}
	a.form(formSpec{
		title: "Add " + what,
		hint:  valueHint,
		build: func(f *tview.Form) {
			if intoObject {
				f.AddInputField("Key", "", 40, nil, nil)
				placeholder(f, "Key", "field name")
			}
			f.AddDropDown("Type", kindNames, 0, nil)
			f.AddInputField("Value", "", 52, nil, nil)
		},
		onOK: func(f *tview.Form) error {
			key := ""
			if intoObject {
				key = inputText(f, "Key")
				if key == "" {
					return fmt.Errorf("a key is required")
				}
			}
			child := &jsontree.Node{}
			if err := applyValue(child, dropdownValue(f, "Type"), inputText(f, "Value")); err != nil {
				return err
			}
			if err := jsontree.AddChild(parent, key, child); err != nil {
				return err
			}
			// rebuild to materialize the new tview node
			v.buildTree()
			if tn, ok := v.cur.nodeMap[child]; ok {
				v.tree.SetCurrentNode(tn)
			}
			v.markDirty(parent)
			return nil
		},
	})
}

// renameNode renames an object member key.
func (a *App) renameNode() {
	v := a.viewer
	jn := v.selectedNode()
	if jn == nil || !v.cur.editable {
		return
	}
	if jn.Parent == nil || jn.Parent.Kind != jsontree.Object {
		a.status.Info("Only object fields can be renamed")
		return
	}
	a.form(formSpec{
		title: "Rename " + jn.Path(),
		hint:  "The field keeps its value and position in the object.",
		build: func(f *tview.Form) {
			f.AddInputField("New key", jn.Key, 40, nil, nil)
		},
		onOK: func(f *tview.Form) error {
			if err := jsontree.Rename(jn, inputText(f, "New key")); err != nil {
				return err
			}
			v.markDirty(jn)
			return nil
		},
	})
}

// deleteNode removes the selected field/element from the tree (goes dirty).
func (a *App) deleteNode() {
	v := a.viewer
	jn := v.selectedNode()
	if jn == nil || !v.cur.editable {
		return
	}
	if jn.Parent == nil {
		a.status.Info("The root value cannot be deleted")
		return
	}
	parent := jn.Parent
	a.confirm("Delete field", fmt.Sprintf("Remove %s from the document?\n\nThis is a local edit; nothing changes in Riak until you save with F2.", jn.Path()), func() {
		if err := jsontree.Delete(jn); err != nil {
			a.status.Err(err)
			return
		}
		v.buildTree()
		if tn, ok := v.cur.nodeMap[parent]; ok {
			v.tree.SetCurrentNode(tn)
		}
		v.markDirty(parent)
	})
}

// ---- key-level CRUD ----

// newKeyDialog creates a new object in the current bucket (F7 at key level).
func (a *App) newKeyDialog() {
	b := a.browser
	if b.level != levelKeys || b.inQuery {
		a.status.Info("Open a bucket's key list to create a key")
		return
	}
	btype, bucket := b.btype, b.bucket
	a.form(formSpec{
		title:   "New key in " + bucketKey(btype, bucket),
		okLabel: "Create",
		hint: "Creation never overwrites: it is sent with If-None-Match: *, so it fails if the key exists. " +
			"JSON bodies are validated before sending. Ctrl-S creates while the body is focused.",
		build: func(f *tview.Form) {
			f.AddInputField("Key", "", 52, nil, nil)
			placeholder(f, "Key", "e.g. user-42")
			f.AddInputField("Content-Type", "application/json", 30, nil, nil)
			f.AddTextArea("Body", "{}", 52, 5, 0, nil)
		},
		onOK: func(f *tview.Form) error {
			key := strings.TrimSpace(inputText(f, "Key"))
			ct := strings.TrimSpace(inputText(f, "Content-Type"))
			body := f.GetFormItemByLabel("Body").(*tview.TextArea).GetText()
			if key == "" {
				return fmt.Errorf("a key is required")
			}
			if strings.Contains(ct, "json") {
				if _, err := jsontree.Parse([]byte(body)); err != nil {
					return fmt.Errorf("body is not valid JSON: %v", err)
				}
			}
			obj := &riak.RiakObject{
				BucketType: btype, Bucket: bucket, Key: key,
				Body: []byte(body), ContentType: ct,
			}
			a.async("creating "+key, 0, func(ctx context.Context) (any, error) {
				return nil, a.client.PutObject(ctx, obj, true)
			}, func(_ any, err error) {
				if err != nil {
					a.alert("Create failed", err.Error())
					return
				}
				a.status.Success("Created %s", key)
				b.loadKeysThen(func() { a.viewer.doLoad(btype, bucket, key) })
			})
			return nil
		},
	})
}

// deleteKeyDialog deletes the key selected in the browser (F8 at key level).
func (a *App) deleteKeyDialog() {
	b := a.browser
	if b.level != levelKeys {
		return
	}
	key := b.currentItem()
	if key == "" {
		return
	}
	btype, bucket := b.btype, b.bucket
	path := bucketKey(btype, bucket) + "/" + key
	a.confirm("Delete key",
		fmt.Sprintf("Permanently delete\n\n%s%s%s\n\nfrom Riak? This cannot be undone.", tDanger(), tview.Escape(path), reset),
		func() {
			vclock := ""
			if c := a.viewer.cur; c != nil && c.obj.Key == key && c.obj.Bucket == bucket {
				vclock = c.obj.Vclock
			}
			a.async("deleting "+key, 0, func(ctx context.Context) (any, error) {
				return nil, a.client.DeleteObject(ctx, btype, bucket, key, vclock)
			}, func(_ any, err error) {
				if err != nil {
					a.alert("Delete failed", err.Error())
					return
				}
				a.status.Success("Deleted %s", path)
				if c := a.viewer.cur; c != nil && c.obj.Key == key && c.obj.Bucket == bucket {
					a.viewer.clear()
				}
				b.loadKeys()
			})
		})
}

// siblingDialog lets the user pick one sibling of a conflicted object. The
// picked sibling is loaded with the 300-response vclock, so saving it
// resolves the conflict.
func (a *App) siblingDialog(btype, bucket, key string, sib *riak.ErrSiblings) {
	list := tview.NewList().ShowSecondaryText(false)
	styleList(list)
	list.SetBackgroundColor(th.Surface)
	for _, vtag := range sib.Vtags {
		vtag := vtag
		list.AddItem(" "+vtag, "", 0, func() {
			a.closeModal()
			a.async("loading sibling "+vtag, 0, func(ctx context.Context) (any, error) {
				obj, err := a.client.GetSibling(ctx, btype, bucket, key, vtag)
				if err == nil {
					obj.Vclock = sib.Vclock // resolving PUT must carry the 300 vclock
				}
				return obj, err
			}, func(res any, err error) {
				if err != nil {
					return
				}
				a.viewer.show(res.(*riak.RiakObject))
				a.viewer.cur.sibling = vtag
				a.viewer.render()
				a.status.Warn("Sibling %s loaded: press F2 to save it and resolve the conflict", vtag)
			})
		})
	}
	frame := newDialogFrame(fmt.Sprintf("%s has %d siblings", tview.Escape(key), len(sib.Vtags)))
	intro := tview.NewTextView().SetDynamicColors(true).SetWordWrap(true)
	intro.SetBackgroundColor(th.Surface)
	intro.SetText(tText() + "Concurrent writes left conflicting versions. Pick one to view; saving it (F2) " +
		"makes it the single resolved value." + reset)
	footer := tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignRight)
	footer.SetBackgroundColor(th.Surface)
	footer.SetText(keyHelp("Enter", "view", "Esc", "cancel"))
	frame.AddItem(intro, 3, 0, false).AddItem(list, 0, 1, true).AddItem(footer, 1, 0, false)
	list.SetDoneFunc(func() { a.closeModal() })
	a.openModal(frame, 66, min(len(sib.Vtags), 12)+7)
}
