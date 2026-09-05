package engine

import (
	"fmt"
	"reflect"
	"sync"

	icore "github.com/brunoga/deep/v6/internal/core"
)

// The engine-facing half of a type family: how to diff two of its values and
// how to apply an operation to one. The matching predicate and the
// equal/clone/codec half live in the core package; the two halves are joined
// by the family name.
//
// A family's Diff returns operations with paths relative to the value ("/" or
// "" for the whole value), using add, remove and replace only. Its Apply
// receives a pointer to a matched value and one such operation, and honours
// op.Strict by verifying op.Old before writing.

type familyOps struct {
	diff    func(a, b any) ([]Operation, error)
	applyOp func(target any, op Operation) error
}

var (
	familyOpsMu  sync.RWMutex
	familyOpsMap = map[string]familyOps{}
)

// RegisterFamilyOps installs the diff and apply handlers for the named family,
// which must also be registered with the core half under the same name.
func RegisterFamilyOps(name string, diff func(a, b any) ([]Operation, error), apply func(target any, op Operation) error) {
	familyOpsMu.Lock()
	defer familyOpsMu.Unlock()
	familyOpsMap[name] = familyOps{diff: diff, applyOp: apply}
}

// familyOpsForType returns the ops for the family owning t, if both halves are
// registered.
func familyOpsForType(t reflect.Type) (familyOps, bool) {
	fam, ok := icore.FamilyFor(t)
	if !ok {
		return familyOps{}, false
	}
	familyOpsMu.RLock()
	ops, ok := familyOpsMap[fam.Name]
	familyOpsMu.RUnlock()
	return ops, ok
}

// familyWireValue returns v in the form it should take inside a serialized
// operation: family-encoded bytes for a family value, v itself otherwise.
func familyWireValue(v any) (any, error) {
	if v == nil || !icore.FamiliesRegistered() {
		return v, nil
	}
	if fam, ok := icore.FamilyFor(reflect.TypeOf(v)); ok && fam.Marshal != nil {
		data, err := fam.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("family %s: %w", fam.Name, err)
		}
		return RawValue{JSON: data}, nil
	}
	return v, nil
}

// ── the diff node ────────────────────────────────────────────────────────────

// familyPatch is the diff of one family-owned value: the operations the
// family's own differ produced, applied by the family's own applier.
type familyPatch struct {
	name    string
	ops     []Operation
	applyOp func(target any, op Operation) error
}

// target turns the position value into the pointer the family applier takes.
func (p *familyPatch) target(v reflect.Value) (any, error) {
	if v.Kind() == reflect.Pointer {
		return v.Interface(), nil
	}
	if v.CanAddr() {
		return v.Addr().Interface(), nil
	}
	return nil, fmt.Errorf("family %s: value at an unaddressable position", p.name)
}

func (p *familyPatch) apply(root, v reflect.Value, path string) {
	_ = p.applyChecked(root, v, false, path)
}

func (p *familyPatch) applyChecked(root, v reflect.Value, strict bool, path string) error {
	t, err := p.target(v)
	if err != nil {
		return err
	}
	for _, op := range p.ops {
		op.Strict = strict
		if err := p.applyOp(t, op); err != nil {
			return err
		}
	}
	return nil
}

func (p *familyPatch) applyResolved(root, v reflect.Value, path string, resolver ConflictResolver) error {
	return p.applyChecked(root, v, false, path)
}

func (p *familyPatch) dependencies(path string) (reads []string, writes []string) {
	return nil, []string{path}
}

func (p *familyPatch) reverse() diffPatch {
	rev := make([]Operation, 0, len(p.ops))
	for i := len(p.ops) - 1; i >= 0; i-- {
		op := p.ops[i]
		switch op.Kind {
		case OpAdd:
			rev = append(rev, Operation{Kind: OpRemove, Path: op.Path, Old: op.New})
		case OpRemove:
			rev = append(rev, Operation{Kind: OpAdd, Path: op.Path, New: op.Old})
		case OpReplace:
			rev = append(rev, Operation{Kind: OpReplace, Path: op.Path, Old: op.New, New: op.Old})
		}
	}
	return &familyPatch{name: p.name, ops: rev, applyOp: p.applyOp}
}

func (p *familyPatch) walk(path string, fn func(path string, op OpKind, old, new any) error) error {
	for _, op := range p.ops {
		if err := fn(joinFamilyPath(path, op.Path), op.Kind, op.Old, op.New); err != nil {
			return err
		}
	}
	return nil
}

func (p *familyPatch) format(indent int) string {
	return fmt.Sprintf("Family(%s, %d ops)", p.name, len(p.ops))
}

func (p *familyPatch) toJSONPatch(path string) []map[string]any {
	out := make([]map[string]any, 0, len(p.ops))
	for _, op := range p.ops {
		m := map[string]any{"op": op.Kind.String(), "path": joinFamilyPath(path, op.Path)}
		if op.Kind == OpAdd || op.Kind == OpReplace {
			if v, err := familyWireValue(op.New); err == nil {
				m["value"] = v
			}
		}
		out = append(out, m)
	}
	return out
}

func (p *familyPatch) summary(path string) string {
	return fmt.Sprintf("%d %s operations at %s", len(p.ops), p.name, path)
}

// joinFamilyPath roots a family-relative operation path at the position the
// family value sits.
func joinFamilyPath(base, rel string) string {
	if rel == "" || rel == "/" {
		if base == "" {
			return "/"
		}
		return base
	}
	return base + rel
}

// ── flat-operation delegation ────────────────────────────────────────────────

// familyDelegate routes a flat operation whose path enters a family-owned
// value to that family's applier: the shortest prefix of the path that lands
// on a family value claims the operation, with the remainder of the path made
// relative to it.
//
// Everything below a family value belongs to its runtime — navigating a
// protobuf message's Go struct directly walks internal bookkeeping — so the
// generic path machinery must not descend past the boundary.
func familyDelegate(v reflect.Value, op Operation) (handled bool, err error) {
	if !icore.FamiliesRegistered() {
		return false, nil
	}
	parts := icore.ParsePath(op.Path)
	for i := 0; i <= len(parts); i++ {
		var cur reflect.Value
		if i == 0 {
			cur = v
		} else {
			prefix := buildPath(parts[:i])
			resolved, rerr := icore.DeepPath(prefix).ResolveMember(v)
			if rerr != nil {
				return false, nil
			}
			cur = resolved
		}
		if !cur.IsValid() {
			return false, nil
		}
		ops, ok := familyOpsForType(cur.Type())
		if !ok || ops.applyOp == nil {
			continue
		}
		rest := buildPath(parts[i:])
		if cur.Kind() == reflect.Pointer && cur.IsNil() {
			// Nothing to apply into. A whole-value replace can still be done
			// by the generic path, which sets the field itself.
			return false, nil
		}
		if rest == "/" && (op.Kind == OpReplace || op.Kind == OpAdd || op.Kind == OpRemove) && i > 0 {
			// The operation addresses the family value itself, not something
			// inside it; setting the field is the generic path's job, and it
			// decodes the value through the family codec on the way.
			return false, nil
		}
		target, terr := (&familyPatch{name: "delegate"}).target(cur)
		if terr != nil {
			return false, nil
		}
		op.Path = rest
		return true, ops.applyOp(target, op)
	}
	return false, nil
}

// buildPath renders path parts back into a JSON Pointer.
func buildPath(parts []icore.PathPart) string {
	if len(parts) == 0 {
		return "/"
	}
	out := ""
	for _, p := range parts {
		if p.IsIndex {
			out += "/" + fmt.Sprint(p.Index)
		} else {
			out += "/" + icore.EscapeKey(p.Key)
		}
	}
	return out
}
