// Command deep-gen generates reflection-free Patch, Diff, Equal and Clone
// methods for the types named by -type.
//
// Field types are classified into four groups, which decide the code emitted
// for them:
//
//   - Structs declared in the generated package (and pointers to them) have the
//     generated methods, so field code calls Equal/Clone/applyOperation on them
//     directly and patches can address sub-paths inside them.
//   - Builtin comparable types, and named types over one, are compared with ==
//     and copied by assignment.
//   - Anything else the generator can name — types from other packages such as
//     time.Time, generic instantiations, arrays, interfaces — is opaque: it is
//     named in type assertions but compared with deep.Equal, which dispatches to
//     the type's own Equal method when it has one.
//   - Types the generator cannot name (channels, funcs, qualifiers that resolve
//     to no import) get no case at all, so applyOperation reports the operation
//     as unhandled and the caller falls back to the reflection engine.
//
// Embedded fields are addressed by their type name, the same way the language
// (and the reflection engine) names them.
//
// The generator also inspects the package's type graph for two facts: cycles,
// and reference classes reachable by more than one route (two *Meta fields, a
// *time.Time next to a map[string]*time.Time, an interface next to any
// pointer). Types where either holds are "shared": their Clone threads a
// deep.CloneMemo so a value reached twice is copied once and cycles are
// rebuilt, their Equal threads a deep.VisitSet so comparison terminates, and
// their Diff threads a deep.DiffMemo so a pair is diffed once and every later
// route becomes one alias operation. Types where neither holds generate
// exactly the code they always did.
//
// Generated code calls Equal, Clone and applyOperation on every struct declared
// in the package that a requested type references — directly, embedded, or as a
// collection element. Those structs must therefore also have generated code:
// list them in -type, or generate them in a separate run over the same package.
//
// Imports are derived from the generated code itself, so a field type that
// refers to another package pulls that package in, and no import is emitted for
// code that ended up not being generated.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/template"
)

var (
	typeNames  = flag.String("type", "", "comma-separated list of type names; must be set")
	outputFile = flag.String("output", "", "output file name; defaults to stdout")
)

// FieldInfo describes one struct field for code generation.
type FieldInfo struct {
	Name     string
	JSONName string
	// Type is the field type rendered as Go source ("*time.Time",
	// "map[string]Detail", ...). It is empty when the generator cannot name the
	// type (channels, funcs, unresolvable package qualifiers); those fields are
	// left to the reflection fallback instead of being named in generated code.
	Type string
	// IsStruct is true for a struct (or pointer to a struct) declared in the
	// package being generated: it has the generated Equal/Clone/applyOperation
	// methods, so field code can delegate to them. Types from other packages
	// never set it — they are handled through Equal/Clone instead.
	IsStruct     bool
	IsCollection bool
	IsText       bool
	// ElemGenerated is true when a slice element or map value is a struct
	// declared in this package, or a pointer to one.
	ElemGenerated bool
	// TypeShared is true when the field's own struct type is in the package's
	// shared set — its values can hold two references to one value — so that
	// type generates the memo-threaded methods and this field's code must call
	// those rather than the plain ones. ElemShared is the same for a
	// collection's element or value type.
	//
	// A field whose type is shared makes the struct holding it shared too, so a
	// set flag here always comes with the threaded form of the enclosing
	// method, where the memo or visited set is in scope.
	TypeShared bool
	ElemShared bool
	// Comparable is true when == compares the field by value: a builtin
	// comparable type, or a named type over one.
	Comparable bool
	// ElemComparable is Comparable for a collection's element or value type.
	ElemComparable bool
	// KnownValue is true for a small set of stdlib types known to have value
	// semantics (time.Time and friends): safe to copy by assignment, but still
	// compared with Equal.
	KnownValue bool
	// ElemKnownValue is KnownValue for a collection's element or value type,
	// looking through one pointer ([]*time.Time counts).
	ElemKnownValue bool
	// PointeeKnown is KnownValue for the pointee of a pointer field.
	PointeeKnown bool
	// Quals holds the package qualifiers Type refers to (e.g. "time").
	Quals    []string
	Ignore   bool
	ReadOnly bool
	Atomic   bool
}

// HasApplyCase reports whether f gets its own case in applyOperation. A field
// whose type cannot be named has no case, so applyOperation reports it as
// unhandled and the caller falls back to the reflection path.
func (f FieldInfo) HasApplyCase() bool { return !f.Ignore && (f.Type != "" || f.ReadOnly) }

// HasCondCase reports whether f gets its own case in evaluateCondition.
func (f FieldInfo) HasCondCase() bool {
	return !f.Ignore && f.Type != "" && !f.IsStruct && !f.IsCollection && !f.IsText
}

// Opaque reports whether f holds a value the generator can name but has no
// generated methods for: a type from another package, a named local non-struct
// type, an interface or an array. Such fields are compared with Equal (which
// dispatches to the type's own Equal method when it has one) rather than ==.
func (f FieldInfo) Opaque() bool {
	return f.Type != "" && !f.IsStruct && !f.IsCollection && !f.IsText && !f.Comparable
}

// Generic reports whether f needs the generic Equal/Clone treatment: either an
// unnameable type or an opaque one.
func (f FieldInfo) Generic() bool { return f.Type == "" || f.Opaque() }

// Generator accumulates generated source for all requested types.
type Generator struct {
	pkgName   string
	pkgPrefix string // "deep." for non-deep packages, "" when generating inside the deep package
	genPrefix string // "gen." — the package holding the generated-code bookkeeping
	buf       bytes.Buffer
	body      bytes.Buffer      // everything below the import block
	typeKeys  map[string]string // typeName -> keyFieldName (from deep:"key" tag)
	idx       pkgIndex          // types declared in this package
	imports   map[string]string // package qualifier -> import path, from the parsed sources
}

// ── template data structs ────────────────────────────────────────────────────

type headerData struct {
	PkgName        string
	NeedsReflect   bool
	NeedsRegexp    bool
	NeedsStrings   bool
	NeedsCondition bool
	NeedsDeep      bool
	NeedsCrdt      bool
	NeedsGen       bool
	Extra          []importSpec
}

// importSpec is an import the generated file needs because a field type refers
// to another package.
type importSpec struct {
	Alias string // empty when the package name matches the path's base
	Path  string
}

type typeData struct {
	TypeName string
	P        string // package prefix for deep
	G        string // package prefix for the generated-code bookkeeping
	Fields   []FieldInfo
	TypeKeys map[string]string
	// Shared is true when values of the type can hold two references to the
	// same value — through a cycle, or through two routes to one pointer.
	// Clone then threads a CloneMemo, Equal a VisitSet and Diff a DiffMemo, so
	// that shared values are copied once, cycles terminate, and every route to
	// a changed value ends up correct after an apply.
	Shared bool
}

// ── helpers used by both templates and FuncMap ───────────────────────────────

// cloneCall and equalCall render a call to the generated method of another
// struct in this package. When the callee is itself in the shared set they call
// the threaded form, which carries the memo or visited set down with it;
// otherwise they call the plain exported method, which is what the type
// generated before sharing support and costs nothing extra.
func cloneCall(recv string, shared bool) string {
	if shared {
		return recv + ".cloneShared(memo)"
	}
	return recv + ".Clone()"
}

func equalCall(recv, arg string, shared bool) string {
	if shared {
		return fmt.Sprintf("%s.equalShared(%s, seen)", recv, arg)
	}
	return fmt.Sprintf("%s.Equal(%s)", recv, arg)
}

func isPtr(s string) bool          { return strings.HasPrefix(s, "*") }
func mapVal(s string) string       { return s[strings.Index(s, "]")+1:] }
func sliceElem(s string) string    { return s[2:] }
func isMapStringKey(s string) bool { return strings.HasPrefix(s, "map[string]") }

func isNumericType(t string) bool {
	switch t {
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64",
		"float32", "float64":
		return true
	}
	return false
}

// isReferenceType reports whether t is a slice or map: a value an assignment
// would share rather than copy.
func isReferenceType(t string) bool {
	return strings.HasPrefix(t, "[]") || strings.HasPrefix(t, "map[")
}

// elemNeedsCopy reports whether a collection element of type t needs copying of
// its own, rather than the plain assignment a value type is content with.
func elemNeedsCopy(f FieldInfo, t string) bool {
	if isPtr(t) || f.ElemGenerated || isReferenceType(t) {
		return true
	}
	// Opaque elements may hold references; only assignment-safe ones are
	// bulk-copied.
	return !f.ElemComparable && !f.ElemKnownValue && !isComparableArray(t)
}

// isBuiltinComparable reports whether == is the right comparison for t: a
// builtin type where == is both valid and means value equality. Pointers,
// named types from other packages and composite types are deliberately
// excluded — they go through Equal, so a type's own Equal method (or a deep
// comparison) is used instead.
func isBuiltinComparable(t string) bool {
	switch t {
	case "string", "bool", "byte", "rune", "uintptr", "complex64", "complex128":
		return true
	}
	return isNumericType(t)
}

// ── FuncMap functions that return code fragments ─────────────────────────────

// fieldApplyCase returns the full `case "/name":` block for ApplyOperation.
func fieldApplyCase(f FieldInfo, p string) string {
	var b strings.Builder
	if f.JSONName != f.Name {
		fmt.Fprintf(&b, "\tcase \"/%s\", \"/%s\":\n", f.JSONName, f.Name)
	} else {
		fmt.Fprintf(&b, "\tcase \"/%s\":\n", f.Name)
	}
	if f.ReadOnly {
		b.WriteString("\t\treturn true, fmt.Errorf(\"field %s is read-only\", op.Path)\n")
		return b.String()
	}
	// OpLog is handled once at the top of applyOperation, before the path
	// switch, so cases never see it.
	// Strict check. The fast form is a type assertion, which holds when the
	// patch was built in this process. A patch that arrived over the wire
	// carries decoded shapes instead — every number a float64, a nested struct
	// a map[string]any — so a failed assertion falls back to the coercing
	// comparison rather than reporting a mismatch that is not there.
	fmt.Fprintf(&b, "\t\tif op.Kind == %sOpReplace && op.Strict && op.Old != nil {\n", p)
	b.WriteString("\t\t\t_match := false\n")
	if f.IsStruct || f.IsText || f.IsCollection || f.Opaque() {
		fmt.Fprintf(&b, "\t\t\tif old, ok := op.Old.(%s); ok { _match = %sEqual(t.%s, old) }\n", f.Type, p, f.Name)
	} else {
		fmt.Fprintf(&b, "\t\t\tif _oldV, ok := op.Old.(%s); ok { _match = t.%s == _oldV }\n", f.Type, f.Name)
	}
	fmt.Fprintf(&b, "\t\t\tif !_match { _match = %sEqualCoerced(t.%s, op.Old) }\n", p, f.Name)
	b.WriteString("\t\t\tif !_match {\n")
	fmt.Fprintf(&b, "\t\t\t\treturn true, fmt.Errorf(\"strict check failed at %%s: expected %%v, got %%v\", op.Path, op.Old, t.%s)\n", f.Name)
	b.WriteString("\t\t\t}\n")
	b.WriteString("\t\t}\n")
	// Value assignment
	if f.IsText {
		// Text is a convergent CRDT type — delegate via Patch with a single-op sub-patch.
		fmt.Fprintf(&b, "\t\top.Path = \"/\"\n")
		fmt.Fprintf(&b, "\t\treturn true, t.%s.Patch(%sPatch[crdt.Text]{Operations: []%sOperation{op}}, logger)\n", f.Name, p, p)
		return b.String()
	}
	// Only add and replace assign; remove, move and copy fall through to the
	// reflection path, which owns their semantics.
	fmt.Fprintf(&b, "\t\tif op.Kind == %sOpAdd || op.Kind == %sOpReplace {\n", p, p)
	fmt.Fprintf(&b, "\t\t\tif v, ok := op.New.(%s); ok {\n\t\t\t\tt.%s = v\n\t\t\t\treturn true, nil\n\t\t\t}\n", f.Type, f.Name)
	// Numeric float64 fallback (JSON deserialises numbers as float64)
	if f.Type == "int" || f.Type == "int64" || f.Type == "float64" {
		fmt.Fprintf(&b, "\t\t\tif f, ok := op.New.(float64); ok {\n\t\t\t\tt.%s = %s(f)\n\t\t\t\treturn true, nil\n\t\t\t}\n", f.Name, f.Type)
	}
	b.WriteString("\t\t}\n")
	return b.String()
}

// delegateCase returns the sub-path delegation block for the default: branch.
func delegateCase(f FieldInfo, p string) string {
	if f.Ignore || f.Atomic {
		return ""
	}
	var b strings.Builder
	if f.IsStruct {
		fmt.Fprintf(&b, "\t\tif strings.HasPrefix(op.Path, \"/%s/\") {\n", f.JSONName)
		if f.ReadOnly {
			b.WriteString("\t\t\treturn true, fmt.Errorf(\"field %s is read-only\", op.Path)\n")
		} else {
			selfArg := "(&t." + f.Name + ")"
			if isPtr(f.Type) {
				selfArg = "t." + f.Name
				fmt.Fprintf(&b, "\t\t\tif %s != nil {\n", selfArg)
				fmt.Fprintf(&b, "\t\t\t\top.Path = op.Path[len(\"/%s/\")-1:]\n", f.JSONName)
				fmt.Fprintf(&b, "\t\t\t\treturn %s.applyOperation(op, logger)\n\t\t\t}\n", selfArg)
			} else {
				fmt.Fprintf(&b, "\t\t\top.Path = op.Path[len(\"/%s/\")-1:]\n", f.JSONName)
				fmt.Fprintf(&b, "\t\t\treturn %s.applyOperation(op, logger)\n", selfArg)
			}
		}
		b.WriteString("\t\t}\n")
	}
	if f.IsCollection && isMapStringKey(f.Type) {
		vt := mapVal(f.Type)
		fmt.Fprintf(&b, "\t\tif strings.HasPrefix(op.Path, \"/%s/\") {\n", f.JSONName)
		if f.ReadOnly {
			b.WriteString("\t\t\treturn true, fmt.Errorf(\"field %s is read-only\", op.Path)\n")
		} else if isPtr(vt) && f.ElemGenerated {
			fmt.Fprintf(&b, "\t\t\tparts := strings.Split(op.Path[len(\"/%s/\"):], \"/\")\n", f.JSONName)
			fmt.Fprintf(&b, "\t\t\tkey := %sUnescapePathKey(parts[0])\n", p)
			// Entry-level operations are handled here; deeper paths delegate to
			// the element's own applyOperation. Anything else (e.g. a
			// JSON-decoded New value) falls through to the reflection path.
			// Strict entry-level ops take the reflection path, which verifies
			// the Old value with full deep-equality semantics.
			//
			// The entry-level test is separate from the delegation below
			// rather than its if/else partner: a strict whole-entry operation
			// belongs to reflection, and routing it into the element instead
			// handed the element a path of "/", which it cannot remove at.
			b.WriteString("\t\t\tif len(parts) == 1 {\n\t\t\tif !op.Strict {\n")
			fmt.Fprintf(&b, "\t\t\t\tif op.Kind == %sOpRemove {\n", p)
			fmt.Fprintf(&b, "\t\t\t\t\tdelete(t.%s, key)\n\t\t\t\t\treturn true, nil\n\t\t\t\t}\n", f.Name)
			fmt.Fprintf(&b, "\t\t\t\tif v, ok := op.New.(%s); ok && (op.Kind == %sOpAdd || op.Kind == %sOpReplace) {\n", vt, p, p)
			fmt.Fprintf(&b, "\t\t\t\t\tif t.%s == nil { t.%s = make(%s) }\n", f.Name, f.Name, f.Type)
			fmt.Fprintf(&b, "\t\t\t\t\tt.%s[key] = v\n\t\t\t\t\treturn true, nil\n\t\t\t\t}\n", f.Name)
			b.WriteString("\t\t\t}\n\t\t\t} else if val, ok := t." + f.Name + "[key]; ok && val != nil {\n")
			b.WriteString("\t\t\t\top.Path = \"/\" + strings.Join(parts[1:], \"/\")\n")
			b.WriteString("\t\t\t\treturn val.applyOperation(op, logger)\n\t\t\t}\n")
		} else {
			fmt.Fprintf(&b, "\t\t\tparts := strings.Split(op.Path[len(\"/%s/\"):], \"/\")\n", f.JSONName)
			fmt.Fprintf(&b, "\t\t\tkey := %sUnescapePathKey(parts[0])\n", p)
			// Only entry-level operations are handled here: a deeper path
			// (e.g. removing one key inside a map-valued entry) must not
			// delete or replace the whole entry — leave it to reflection.
			// Strict entry-level ops take the reflection path, which verifies
			// the Old value with full deep-equality semantics.
			b.WriteString("\t\t\tif len(parts) == 1 && !op.Strict {\n")
			fmt.Fprintf(&b, "\t\t\t\tif op.Kind == %sOpRemove {\n", p)
			fmt.Fprintf(&b, "\t\t\t\t\tdelete(t.%s, key)\n\t\t\t\t\treturn true, nil\n\t\t\t\t}\n", f.Name)
			fmt.Fprintf(&b, "\t\t\t\tif v, ok := op.New.(%s); ok && (op.Kind == %sOpAdd || op.Kind == %sOpReplace) {\n", vt, p, p)
			fmt.Fprintf(&b, "\t\t\t\t\tif t.%s == nil { t.%s = make(%s) }\n", f.Name, f.Name, f.Type)
			fmt.Fprintf(&b, "\t\t\t\t\tt.%s[key] = v\n\t\t\t\t\treturn true, nil\n\t\t\t\t}\n", f.Name)
			b.WriteString("\t\t\t}\n")
		}
		b.WriteString("\t\t}\n")
	}
	return b.String()
}

// diffFieldCode returns the diff fragment for one field. shared is true when
// the enclosing type threads a DiffMemo; its diffShared emits absolute paths (a
// variable `at` holds the type's own path), so every path this fragment builds
// is prefixed with it on the way out — see diffFieldPaths.
func diffFieldCode(f FieldInfo, p, g string, typeKeys map[string]string, shared bool) string {
	code := diffFieldBody(f, p, g, typeKeys)
	if !shared {
		return code
	}
	return diffFieldPaths(code)
}

// diffFieldPaths re-roots every path expression in a relative diff fragment on
// the `at` variable. The fragment is machine-written by diffFieldBody, so its
// path expressions take exactly three shapes: an Operation's Path field, a
// keyed-slice `prefix` variable, and the re-rooting of a memo-free sub-diff's
// operations.
func diffFieldPaths(code string) string {
	code = strings.ReplaceAll(code, `Path: "`, `Path: at + "`)
	code = strings.ReplaceAll(code, `Path: fmt.Sprintf("`, `Path: at + fmt.Sprintf("`)
	code = strings.ReplaceAll(code, `prefix := "`, `prefix := at + "`)
	code = strings.ReplaceAll(code, `op.Path = "`, `op.Path = at + "`)
	return code
}

// diffFieldBody renders the diff fragment with paths relative to the type.
func diffFieldBody(f FieldInfo, p, g string, typeKeys map[string]string) string {
	var b strings.Builder
	if f.Ignore {
		return ""
	}
	if (f.IsStruct || f.IsText) && !f.Atomic {
		self, other := "(&t."+f.Name+")", "&other."+f.Name
		if isPtr(f.Type) {
			self, other = "t."+f.Name, "other."+f.Name
		}
		if f.IsText {
			other = "other." + f.Name
		}
		// Text is a slice value — &t.Field is never nil and Text.Diff handles empty slices.
		// Only pointer fields need nil handling: a pointer appearing emits an
		// add, a pointer disappearing emits a remove, and both-non-nil
		// delegates to the element's Diff.
		needsGuard := isPtr(f.Type)
		if needsGuard {
			fmt.Fprintf(&b, "\tif %s == nil && %s != nil {\n", self, other)
			fmt.Fprintf(&b, "\t\tp.Operations = append(p.Operations, %sOperation{Kind: %sOpAdd, Path: \"/%s\", New: %s})\n", p, p, f.JSONName, other)
			fmt.Fprintf(&b, "\t} else if %s != nil && %s == nil {\n", self, other)
			fmt.Fprintf(&b, "\t\tp.Operations = append(p.Operations, %sOperation{Kind: %sOpRemove, Path: \"/%s\", Old: %s})\n", p, p, f.JSONName, self)
			fmt.Fprintf(&b, "\t} else if %s != nil && %s != nil {\n", self, other)
		}
		if f.TypeShared {
			// The callee threads the memo and emits absolute paths itself, so
			// its operations are appended as they are.
			fmt.Fprintf(&b, "\t\tsub%s := %s.diffShared(%s, seen, at + \"/%s\")\n", f.Name, self, other, f.JSONName)
			fmt.Fprintf(&b, "\t\tp.Operations = append(p.Operations, sub%s.Operations...)\n", f.Name)
		} else {
			// Text and memo-free structs diff standalone with relative paths;
			// their operations are re-rooted here.
			fmt.Fprintf(&b, "\t\tsub%s := %s.Diff(%s)\n", f.Name, self, other)
			fmt.Fprintf(&b, "\t\tfor _, op := range sub%s.Operations {\n", f.Name)
			fmt.Fprintf(&b, "\t\t\tif op.Path == \"\" || op.Path == \"/\" { op.Path = \"/%s\" } else { op.Path = \"/%s\" + op.Path }\n", f.JSONName, f.JSONName)
			b.WriteString("\t\t\tp.Operations = append(p.Operations, op)\n\t\t}\n")
		}
		if needsGuard {
			b.WriteString("\t}\n")
		}
	} else if f.IsCollection && !f.Atomic {
		if strings.HasPrefix(f.Type, "map[") {
			vt := mapVal(f.Type)
			ptrVal := isPtr(vt)
			// Everything this field emits is bracketed so the range can be
			// put in a fixed order at the end. Go randomises map iteration,
			// and a patch whose operation order varies between runs cannot be
			// logged, cached, compared or signed. Ordering the operations
			// costs one sort over the entries that changed; ordering the keys
			// first would cost a slice, a sort and an extra lookup for every
			// entry in the map, changed or not.
			b.WriteString("\t{\n")
			b.WriteString("\t_mapFrom := len(p.Operations)\n")
			fmt.Fprintf(&b, "\tif (t.%s == nil) != (other.%s == nil) {\n", f.Name, f.Name)
			fmt.Fprintf(&b, "\t\tp.Operations = append(p.Operations, %sOperation{Kind: %sOpReplace, Path: \"/%s\", Old: t.%s, New: other.%s})\n", p, p, f.JSONName, f.Name, f.Name)
			b.WriteString("\t} else {\n")
			fmt.Fprintf(&b, "\tif other.%s != nil {\n", f.Name)
			fmt.Fprintf(&b, "\t\tfor k, v := range other.%s {\n", f.Name)
			fmt.Fprintf(&b, "\t\t\tif t.%s == nil {\n", f.Name)
			fmt.Fprintf(&b, "\t\t\t\tp.Operations = append(p.Operations, %sOperation{Kind: %sOpReplace, Path: \"/%s/\" + %sEscapePathKey(fmt.Sprintf(\"%%v\", k)), New: v})\n", p, p, f.JSONName, p)
			b.WriteString("\t\t\t\tcontinue\n\t\t\t}\n")
			if ptrVal && f.ElemGenerated && f.ElemShared {
				// Pointer values of a shared type are diffed in place, like
				// struct fields: that tracks the pair, so a value reachable
				// both through this map and elsewhere is diffed once and
				// aliased at the other routes.
				fmt.Fprintf(&b, "\t\t\toldV, ok := t.%s[k]\n", f.Name)
				b.WriteString("\t\t\tswitch {\n")
				b.WriteString("\t\t\tcase !ok:\n")
				fmt.Fprintf(&b, "\t\t\t\tp.Operations = append(p.Operations, %sOperation{Kind: %sOpAdd, Path: \"/%s/\" + %sEscapePathKey(fmt.Sprintf(\"%%v\", k)), New: v})\n", p, p, f.JSONName, p)
				b.WriteString("\t\t\tcase (oldV == nil) != (v == nil):\n")
				fmt.Fprintf(&b, "\t\t\t\tp.Operations = append(p.Operations, %sOperation{Kind: %sOpReplace, Path: \"/%s/\" + %sEscapePathKey(fmt.Sprintf(\"%%v\", k)), Old: oldV, New: v})\n", p, p, f.JSONName, p)
				b.WriteString("\t\t\tcase oldV != nil:\n")
				fmt.Fprintf(&b, "\t\t\t\tsub := oldV.diffShared(v, seen, at + \"/%s/\" + %sEscapePathKey(fmt.Sprintf(\"%%v\", k)))\n", f.JSONName, p)
				b.WriteString("\t\t\t\tp.Operations = append(p.Operations, sub.Operations...)\n")
				b.WriteString("\t\t\t}\n\t\t}\n\t}\n")
			} else {
				fmt.Fprintf(&b, "\t\t\tif oldV, ok := t.%s[k]; !ok || ", f.Name)
				switch {
				case ptrVal && f.ElemGenerated:
					b.WriteString("!oldV.Equal(v) {\n")
				case f.ElemGenerated:
					b.WriteString("!oldV.Equal(&v) {\n")
				case f.ElemComparable:
					b.WriteString("v != oldV {\n")
				default:
					fmt.Fprintf(&b, "!%sEqual(oldV, v) {\n", p)
				}
				fmt.Fprintf(&b, "\t\t\t\tkind := %sOpReplace\n\t\t\t\tif !ok { kind = %sOpAdd }\n", p, p)
				fmt.Fprintf(&b, "\t\t\t\tp.Operations = append(p.Operations, %sOperation{Kind: kind, Path: \"/%s/\" + %sEscapePathKey(fmt.Sprintf(\"%%v\", k)), Old: oldV, New: v})\n", p, f.JSONName, p)
				b.WriteString("\t\t\t}\n\t\t}\n\t}\n")
			}
			fmt.Fprintf(&b, "\tif t.%s != nil {\n", f.Name)
			fmt.Fprintf(&b, "\t\tfor k, v := range t.%s {\n", f.Name)
			fmt.Fprintf(&b, "\t\t\tif _, ok := other.%s[k]; !ok {\n", f.Name)
			fmt.Fprintf(&b, "\t\t\t\tp.Operations = append(p.Operations, %sOperation{Kind: %sOpRemove, Path: \"/%s/\" + %sEscapePathKey(fmt.Sprintf(\"%%v\", k)), Old: v})\n", p, p, f.JSONName, p)
			b.WriteString("\t\t\t}\n\t\t}\n\t}\n")
			b.WriteString("\t}\n")
			fmt.Fprintf(&b, "\t%sSortOperations(p.Operations[_mapFrom:])\n", g)
			b.WriteString("\t}\n")
		} else {
			// Slice
			elemType := sliceElem(f.Type)
			keyField := typeKeys[elemType]
			if keyField != "" {
				// Keyed slice diff. Braces scope the per-field index maps so
				// several keyed slices in one struct do not collide.
				b.WriteString("\t{\n")
				// As for maps: per-key operations cannot turn an empty keyed
				// slice into a nil one.
				fmt.Fprintf(&b, "\tif (t.%s == nil) != (other.%s == nil) {\n", f.Name, f.Name)
				fmt.Fprintf(&b, "\t\tp.Operations = append(p.Operations, %sOperation{Kind: %sOpReplace, Path: \"/%s\", Old: t.%s, New: other.%s})\n", p, p, f.JSONName, f.Name, f.Name)
				b.WriteString("\t} else {\n")
				fmt.Fprintf(&b, "\totherByKey := make(map[any]int)\n")
				fmt.Fprintf(&b, "\tfor i, v := range other.%s { otherByKey[v.%s] = i }\n", f.Name, keyField)
				fmt.Fprintf(&b, "\tfor _, v := range t.%s {\n", f.Name)
				fmt.Fprintf(&b, "\t\tif _, ok := otherByKey[v.%s]; !ok {\n", keyField)
				fmt.Fprintf(&b, "\t\t\tp.Operations = append(p.Operations, %sOperation{Kind: %sOpRemove, Path: \"/%s/\" + %sEscapePathKey(fmt.Sprintf(\"%%v\", v.%s)), Old: v})\n", p, p, f.JSONName, p, keyField)
				b.WriteString("\t\t}\n\t}\n")
				fmt.Fprintf(&b, "\ttByKey := make(map[any]int)\n")
				fmt.Fprintf(&b, "\tfor i, v := range t.%s { tByKey[v.%s] = i }\n", f.Name, keyField)
				fmt.Fprintf(&b, "\tfor j := range other.%s {\n", f.Name)
				fmt.Fprintf(&b, "\t\ti, ok := tByKey[other.%s[j].%s]\n", f.Name, keyField)
				b.WriteString("\t\tif !ok {\n")
				fmt.Fprintf(&b, "\t\t\tp.Operations = append(p.Operations, %sOperation{Kind: %sOpAdd, Path: \"/%s/\" + %sEscapePathKey(fmt.Sprintf(\"%%v\", other.%s[j].%s)), New: other.%s[j]})\n", p, p, f.JSONName, p, f.Name, keyField, f.Name)
				b.WriteString("\t\t\tcontinue\n\t\t}\n")
				// A key present on both sides may still have changed content.
				fmt.Fprintf(&b, "\t\tprefix := \"/%s/\" + %sEscapePathKey(fmt.Sprintf(\"%%v\", other.%s[j].%s))\n", f.JSONName, p, f.Name, keyField)
				if f.ElemGenerated && f.ElemShared {
					fmt.Fprintf(&b, "\t\tsub := (&t.%s[i]).diffShared(&other.%s[j], seen, prefix)\n", f.Name, f.Name)
					b.WriteString("\t\tp.Operations = append(p.Operations, sub.Operations...)\n")
				} else if f.ElemGenerated {
					fmt.Fprintf(&b, "\t\tsub := t.%s[i].Diff(&other.%s[j])\n", f.Name, f.Name)
					b.WriteString("\t\tfor _, op := range sub.Operations {\n")
					b.WriteString("\t\t\tif op.Path == \"\" || op.Path == \"/\" { op.Path = prefix } else { op.Path = prefix + op.Path }\n")
					b.WriteString("\t\t\tp.Operations = append(p.Operations, op)\n\t\t}\n")
				} else {
					fmt.Fprintf(&b, "\t\tif !%sEqual(t.%s[i], other.%s[j]) {\n", p, f.Name, f.Name)
					fmt.Fprintf(&b, "\t\t\tp.Operations = append(p.Operations, %sOperation{Kind: %sOpReplace, Path: prefix, Old: t.%s[i], New: other.%s[j]})\n", p, p, f.Name, f.Name)
					b.WriteString("\t\t}\n")
				}
				b.WriteString("\t}\n")
				b.WriteString("\t}\n")
				b.WriteString("\t}\n")
			} else {
				fmt.Fprintf(&b, "\tif len(t.%s) != len(other.%s) || (t.%s == nil) != (other.%s == nil) {\n", f.Name, f.Name, f.Name, f.Name)
				fmt.Fprintf(&b, "\t\tp.Operations = append(p.Operations, %sOperation{Kind: %sOpReplace, Path: \"/%s\", Old: t.%s, New: other.%s})\n", p, p, f.JSONName, f.Name, f.Name)
				b.WriteString("\t} else {\n")
				fmt.Fprintf(&b, "\t\tfor i := range t.%s {\n", f.Name)
				if f.ElemGenerated && isPtr(elemType) && f.ElemShared {
					// As for maps: shared pointer elements are diffed in
					// place so the pair is tracked and aliases fire.
					fmt.Fprintf(&b, "\t\t\tif (t.%s[i] == nil) != (other.%s[i] == nil) {\n", f.Name, f.Name)
					fmt.Fprintf(&b, "\t\t\t\tp.Operations = append(p.Operations, %sOperation{Kind: %sOpReplace, Path: fmt.Sprintf(\"/%s/%%d\", i), Old: t.%s[i], New: other.%s[i]})\n", p, p, f.JSONName, f.Name, f.Name)
					fmt.Fprintf(&b, "\t\t\t} else if t.%s[i] != nil {\n", f.Name)
					fmt.Fprintf(&b, "\t\t\t\tsub := t.%s[i].diffShared(other.%s[i], seen, at + fmt.Sprintf(\"/%s/%%d\", i))\n", f.Name, f.Name, f.JSONName)
					b.WriteString("\t\t\t\tp.Operations = append(p.Operations, sub.Operations...)\n")
					b.WriteString("\t\t\t}\n\t\t}\n\t}\n")
					return b.String()
				}
				switch {
				case f.ElemGenerated && isPtr(elemType):
					fmt.Fprintf(&b, "\t\t\tif (t.%s[i] == nil) != (other.%s[i] == nil) || (t.%s[i] != nil && !t.%s[i].Equal(other.%s[i])) {\n",
						f.Name, f.Name, f.Name, f.Name, f.Name)
				case f.ElemGenerated:
					fmt.Fprintf(&b, "\t\t\tif !t.%s[i].Equal(&other.%s[i]) {\n", f.Name, f.Name)
				case f.ElemComparable:
					fmt.Fprintf(&b, "\t\t\tif t.%s[i] != other.%s[i] {\n", f.Name, f.Name)
				default:
					fmt.Fprintf(&b, "\t\t\tif !%sEqual(t.%s[i], other.%s[i]) {\n", p, f.Name, f.Name)
				}
				fmt.Fprintf(&b, "\t\t\t\tp.Operations = append(p.Operations, %sOperation{Kind: %sOpReplace, Path: fmt.Sprintf(\"/%s/%%d\", i), Old: t.%s[i], New: other.%s[i]})\n", p, p, f.JSONName, f.Name, f.Name)
				b.WriteString("\t\t\t}\n\t\t}\n\t}\n")
			}
		}
	} else if f.Generic() || f.Atomic && (f.IsStruct || f.IsCollection || f.IsText) {
		// Atomic composite fields diff as a single whole-value replace; == is
		// not even defined for most of them.
		fmt.Fprintf(&b, "\tif !%sEqual(t.%s, other.%s) {\n", p, f.Name, f.Name)
		fmt.Fprintf(&b, "\t\tp.Operations = append(p.Operations, %sOperation{Kind: %sOpReplace, Path: \"/%s\", Old: t.%s, New: other.%s})\n", p, p, f.JSONName, f.Name, f.Name)
		b.WriteString("\t}\n")
	} else {
		fmt.Fprintf(&b, "\tif t.%s != other.%s {\n", f.Name, f.Name)
		fmt.Fprintf(&b, "\t\tp.Operations = append(p.Operations, %sOperation{Kind: %sOpReplace, Path: \"/%s\", Old: t.%s, New: other.%s})\n", p, p, f.JSONName, f.Name, f.Name)
		b.WriteString("\t}\n")
	}
	return b.String()
}

// evalCondCase returns the case body for EvaluateCondition's path switch.
func evalCondCase(f FieldInfo, pkgPrefix string) string {
	var b strings.Builder
	n, typ := f.Name, f.Type

	b.WriteString("\t\tif c.Op == \"exists\" { return true, nil }\n")
	fmt.Fprintf(&b, "\t\tif c.Op == \"type\" {\n")
	b.WriteString("\t\t\ttn, ok := c.Value.(string)\n")
	b.WriteString("\t\t\tif !ok { return false, fmt.Errorf(\"type requires string value\") }\n")
	fmt.Fprintf(&b, "\t\t\treturn condition.CheckType(t.%s, tn), nil\n\t\t}\n", n)
	fmt.Fprintf(&b, "\t\tif c.Op == \"matches\" {\n")
	b.WriteString("\t\t\tpat, ok := c.Value.(string)\n")
	b.WriteString("\t\t\tif !ok { return false, fmt.Errorf(\"matches requires string pattern\") }\n")
	fmt.Fprintf(&b, "\t\t\treturn regexp.MatchString(pat, fmt.Sprintf(\"%%v\", t.%s))\n\t\t}\n", n)

	switch {
	case isNumericType(typ):
		b.WriteString("\t\tvar _cv float64\n")
		b.WriteString("\t\tswitch v := c.Value.(type) {\n")
		fmt.Fprintf(&b, "\t\tcase %s: _cv = float64(v)\n", typ)
		if typ != "float64" {
			b.WriteString("\t\tcase float64: _cv = v\n")
		}
		if typ != "int" {
			b.WriteString("\t\tcase int: _cv = float64(v)\n")
		}
		fmt.Fprintf(&b, "\t\tdefault: return false, fmt.Errorf(\"condition value type mismatch for field %s\")\n", n)
		b.WriteString("\t\t}\n")
		fmt.Fprintf(&b, "\t\t_fv := float64(t.%s)\n", n)
		b.WriteString("\t\tswitch c.Op {\n")
		b.WriteString("\t\tcase \"==\": return _fv == _cv, nil\n")
		b.WriteString("\t\tcase \"!=\": return _fv != _cv, nil\n")
		b.WriteString("\t\tcase \">\":  return _fv > _cv, nil\n")
		b.WriteString("\t\tcase \"<\":  return _fv < _cv, nil\n")
		b.WriteString("\t\tcase \">=\": return _fv >= _cv, nil\n")
		b.WriteString("\t\tcase \"<=\": return _fv <= _cv, nil\n")
		b.WriteString("\t\tcase \"in\":\n")
		fmt.Fprintf(&b, "\t\t\tswitch vals := c.Value.(type) {\n\t\t\tcase []%s:\n\t\t\t\tfor _, v := range vals { if t.%s == v { return true, nil } }\n", typ, n)
		b.WriteString("\t\t\tcase []any:\n\t\t\t\tfor _, v := range vals {\n\t\t\t\t\tswitch iv := v.(type) {\n")
		fmt.Fprintf(&b, "\t\t\t\t\tcase %s: if t.%s == iv { return true, nil }\n", typ, n)
		if typ != "float64" {
			fmt.Fprintf(&b, "\t\t\t\t\tcase float64: if float64(t.%s) == iv { return true, nil }\n", n)
		}
		if typ != "int" {
			fmt.Fprintf(&b, "\t\t\t\t\tcase int: if float64(t.%s) == float64(iv) { return true, nil }\n", n)
		}
		b.WriteString("\t\t\t\t\t}\n\t\t\t\t}\n\t\t\t}\n\t\t\treturn false, nil\n\t\t}\n")

	case typ == "string":
		fmt.Fprintf(&b, "\t\t_sv, _ok := c.Value.(string)\n")
		fmt.Fprintf(&b, "\t\tif !_ok { return false, fmt.Errorf(\"condition value type mismatch for field %s\") }\n", n)
		b.WriteString("\t\tswitch c.Op {\n")
		fmt.Fprintf(&b, "\t\tcase \"==\": return t.%s == _sv, nil\n", n)
		fmt.Fprintf(&b, "\t\tcase \"!=\": return t.%s != _sv, nil\n", n)
		fmt.Fprintf(&b, "\t\tcase \">\":  return t.%s > _sv, nil\n", n)
		fmt.Fprintf(&b, "\t\tcase \"<\":  return t.%s < _sv, nil\n", n)
		fmt.Fprintf(&b, "\t\tcase \">=\": return t.%s >= _sv, nil\n", n)
		fmt.Fprintf(&b, "\t\tcase \"<=\": return t.%s <= _sv, nil\n", n)
		b.WriteString("\t\tcase \"in\":\n")
		fmt.Fprintf(&b, "\t\t\tswitch vals := c.Value.(type) {\n\t\t\tcase []string:\n\t\t\t\tfor _, v := range vals { if t.%s == v { return true, nil } }\n", n)
		fmt.Fprintf(&b, "\t\t\tcase []any:\n\t\t\t\tfor _, v := range vals { if sv, ok := v.(string); ok && t.%s == sv { return true, nil } }\n", n)
		b.WriteString("\t\t\t}\n\t\t\treturn false, nil\n\t\t}\n")

	case typ == "bool":
		fmt.Fprintf(&b, "\t\t_bv, _ok := c.Value.(bool)\n")
		fmt.Fprintf(&b, "\t\tif !_ok { return false, fmt.Errorf(\"condition value type mismatch for field %s\") }\n", n)
		b.WriteString("\t\tswitch c.Op {\n")
		fmt.Fprintf(&b, "\t\tcase \"==\": return t.%s == _bv, nil\n", n)
		fmt.Fprintf(&b, "\t\tcase \"!=\": return t.%s != _bv, nil\n", n)
		b.WriteString("\t\t}\n")

	default:
		b.WriteString("\t\tswitch c.Op {\n")
		fmt.Fprintf(&b, "\t\tcase \"==\": return fmt.Sprintf(\"%%v\", t.%s) == fmt.Sprintf(\"%%v\", c.Value), nil\n", n)
		fmt.Fprintf(&b, "\t\tcase \"!=\": return fmt.Sprintf(\"%%v\", t.%s) != fmt.Sprintf(\"%%v\", c.Value), nil\n", n)
		b.WriteString("\t\t}\n")
	}
	return b.String()
}

// equalFieldCode returns the equality check fragment for one field.
func equalFieldCode(f FieldInfo, p string) string {
	var b strings.Builder
	self := "(&t." + f.Name + ")"
	other := "(&other." + f.Name + ")"
	if isPtr(f.Type) {
		self = "t." + f.Name
		other = "other." + f.Name
	}
	switch {
	case f.IsStruct:
		if isPtr(f.Type) {
			fmt.Fprintf(&b, "\tif (%s == nil) != (%s == nil) { return false }\n", self, other)
			fmt.Fprintf(&b, "\tif %s != nil && !%s { return false }\n", self, equalCall(self, other, f.TypeShared))
		} else {
			fmt.Fprintf(&b, "\tif !%s { return false }\n", equalCall(self, other, f.TypeShared))
		}
	case f.IsText:
		fmt.Fprintf(&b, "\tif len(t.%s) != len(other.%s) { return false }\n", f.Name, f.Name)
		fmt.Fprintf(&b, "\tfor i := range t.%s { if t.%s[i] != other.%s[i] { return false } }\n", f.Name, f.Name, f.Name)
	case f.IsCollection:
		fmt.Fprintf(&b, "\tif len(t.%s) != len(other.%s) { return false }\n", f.Name, f.Name)
		if strings.HasPrefix(f.Type, "[]") {
			et := sliceElem(f.Type)
			ptrElem := isPtr(et)
			fmt.Fprintf(&b, "\tfor i := range t.%s {\n", f.Name)
			switch {
			case ptrElem && f.ElemGenerated:
				fmt.Fprintf(&b, "\t\tif (t.%s[i] == nil) != (other.%s[i] == nil) { return false }\n", f.Name, f.Name)
				elem := fmt.Sprintf("t.%s[i]", f.Name)
				fmt.Fprintf(&b, "\t\tif %s != nil && !%s { return false }\n",
					elem, equalCall(elem, fmt.Sprintf("other.%s[i]", f.Name), f.ElemShared))
			case f.ElemGenerated:
				fmt.Fprintf(&b, "\t\tif !%s { return false }\n",
					equalCall(fmt.Sprintf("t.%s[i]", f.Name), fmt.Sprintf("&other.%s[i]", f.Name), f.ElemShared))
			case f.ElemComparable:
				fmt.Fprintf(&b, "\t\tif t.%s[i] != other.%s[i] { return false }\n", f.Name, f.Name)
			default:
				fmt.Fprintf(&b, "\t\tif !%sEqual(t.%s[i], other.%s[i]) { return false }\n", p, f.Name, f.Name)
			}
			b.WriteString("\t}\n")
		} else if strings.HasPrefix(f.Type, "map[") {
			vt := mapVal(f.Type)
			ptrVal := isPtr(vt)
			fmt.Fprintf(&b, "\tfor k, v := range t.%s {\n", f.Name)
			fmt.Fprintf(&b, "\t\tvOther, ok := other.%s[k]\n", f.Name)
			b.WriteString("\t\tif !ok { return false }\n")
			switch {
			case ptrVal && f.ElemGenerated:
				b.WriteString("\t\tif (v == nil) != (vOther == nil) { return false }\n")
				fmt.Fprintf(&b, "\t\tif v != nil && !%s { return false }\n", equalCall("v", "vOther", f.ElemShared))
			case f.ElemGenerated:
				if f.ElemShared {
					// Map values are not addressable, so the visited set would
					// otherwise key on the range variable. Copying into
					// variables declared inside the loop body gives each
					// iteration its own, in every language version.
					b.WriteString("\t\t_e, _o := v, vOther\n")
					fmt.Fprintf(&b, "\t\tif !%s { return false }\n", equalCall("_e", "&_o", f.ElemShared))
				} else {
					b.WriteString("\t\tif !v.Equal(&vOther) { return false }\n")
				}
			case f.ElemComparable:
				b.WriteString("\t\tif v != vOther { return false }\n")
			default:
				fmt.Fprintf(&b, "\t\tif !%sEqual(v, vOther) { return false }\n", p)
			}
			b.WriteString("\t}\n")
		}
	case f.Generic():
		fmt.Fprintf(&b, "\tif !%sEqual(t.%s, other.%s) { return false }\n", p, f.Name, f.Name)
	default:
		fmt.Fprintf(&b, "\tif t.%s != other.%s { return false }\n", f.Name, f.Name)
	}
	return b.String()
}

// copyFieldInit returns the struct-literal initialiser fragment for one field (inside `res := &T{...}`).
func copyFieldInit(f FieldInfo) string {
	switch {
	case f.IsStruct:
		return "" // handled in post-init phase
	case f.Type == "":
		return "" // unnameable type — deep-copied in the post-init phase
	case f.IsText, f.IsCollection:
		// Slices, Text and maps are allocated in the post-init phase, where the
		// copy can be skipped for a nil one. A nil slice and an empty slice are
		// different values — they marshal differently, and the reflection
		// engine's Equal tells them apart — so a copy has to preserve which one
		// it was given.
		return ""
	case f.Opaque() && !f.KnownValue && !isComparableArray(f.Type):
		// A type the generator cannot see inside may hold references; it is
		// deep-copied in the post-init phase.
		return ""
	default:
		return fmt.Sprintf("\t\t%s: t.%s,\n", f.Name, f.Name)
	}
}

// isComparableArray reports whether t is a fixed-size array of a builtin
// comparable element ("[3]int"), which an assignment copies fully.
func isComparableArray(t string) bool {
	if !strings.HasPrefix(t, "[") || strings.HasPrefix(t, "[]") {
		return false
	}
	i := strings.Index(t, "]")
	if i < 0 {
		return false
	}
	return isBuiltinComparable(t[i+1:])
}

// copyFieldPost returns post-init deep-copy code for one field. shared is true
// when the enclosing type threads a CloneMemo; then every pointer the field
// holds goes through the memo, so a value reached by two routes is copied once
// and both routes in the result point at that one copy. Fields the generator
// cannot copy itself are handed to gen.CloneShared, which runs the reflection
// engine against the same memo — one identity space across both.
func copyFieldPost(f FieldInfo, p, g string, shared bool) string {
	var b strings.Builder
	if f.Ignore {
		return ""
	}
	// genericClone renders the deep-copy call for values the generator cannot
	// see inside.
	genericClone := func(arg string) string {
		if shared {
			return fmt.Sprintf("%sCloneShared(%s, memo)", g, arg)
		}
		return fmt.Sprintf("%sClone(%s)", p, arg)
	}
	// memoPtr renders a pointer copy through the memo: reuse the recorded copy
	// or make one with mk (an expression producing the copy of src) and record
	// it. typ is the pointer type for the load assertion.
	memoPtr := func(indent, src, dst, typ, mk string) string {
		return fmt.Sprintf(
			"%sif _d, ok := memo.Load(%s); ok { %s = _d.(%s) } else { %s = %s; memo.Store(%s, %s) }\n",
			indent, src, dst, typ, dst, mk, src, dst)
	}

	if f.Type == "" {
		// The generator cannot name the type, so it cannot copy it structurally.
		fmt.Fprintf(&b, "\tres.%s = %s\n", f.Name, genericClone("t."+f.Name))
		return b.String()
	}
	if f.Opaque() && isPtr(f.Type) {
		if f.PointeeKnown {
			// Pointer to a value-semantics stdlib type: copying the pointee is
			// a full copy.
			if shared {
				fmt.Fprintf(&b, "\tif t.%s != nil {\n", f.Name)
				b.WriteString(memoPtr("\t\t", "t."+f.Name, "res."+f.Name, f.Type,
					fmt.Sprintf("func() %s { _v := *t.%s; return &_v }()", f.Type, f.Name)))
				b.WriteString("\t}\n")
			} else {
				fmt.Fprintf(&b, "\tif t.%s != nil { _v := *t.%s; res.%s = &_v }\n", f.Name, f.Name, f.Name)
			}
		} else {
			// The pointee may hold references of its own.
			fmt.Fprintf(&b, "\tres.%s = %s\n", f.Name, genericClone("t."+f.Name))
		}
		return b.String()
	}
	if f.Opaque() && !f.KnownValue && !isComparableArray(f.Type) {
		// A type the generator cannot see inside (another package's struct, a
		// generic instantiation, an interface): deep-copy it so the clone does
		// not share references with the original.
		fmt.Fprintf(&b, "\tres.%s = %s\n", f.Name, genericClone("t."+f.Name))
		return b.String()
	}
	if f.IsStruct {
		self := "(&t." + f.Name + ")"
		if isPtr(f.Type) {
			self = "t." + f.Name
			switch {
			case f.TypeShared:
				// cloneShared records itself in the memo.
				fmt.Fprintf(&b, "\tif %s != nil { res.%s = %s }\n", self, f.Name, cloneCall(self, true))
			case shared:
				// The callee has nothing shareable inside, but the pointer
				// itself is an identity two routes can share.
				fmt.Fprintf(&b, "\tif %s != nil {\n", self)
				b.WriteString(memoPtr("\t\t", self, "res."+f.Name, f.Type, self+".Clone()"))
				b.WriteString("\t}\n")
			default:
				fmt.Fprintf(&b, "\tif %s != nil { res.%s = %s }\n", self, f.Name, cloneCall(self, false))
			}
		} else {
			fmt.Fprintf(&b, "\tres.%s = *%s\n", f.Name, cloneCall(self, f.TypeShared))
		}
	}
	if f.IsText {
		// Text is a slice; a nil one stays nil.
		fmt.Fprintf(&b, "\tif t.%s != nil {\n", f.Name)
		fmt.Fprintf(&b, "\t\tres.%s = make(%s, len(t.%s))\n", f.Name, f.Type, f.Name)
		fmt.Fprintf(&b, "\t\tcopy(res.%s, t.%s)\n\t}\n", f.Name, f.Name)
		return b.String()
	}
	if f.IsCollection {
		if strings.HasPrefix(f.Type, "[]") {
			et := sliceElem(f.Type)
			// The slice is allocated here rather than in the struct literal so
			// that a nil one stays nil instead of becoming an empty slice.
			fmt.Fprintf(&b, "\tif t.%s != nil {\n", f.Name)
			fmt.Fprintf(&b, "\t\tres.%s = make(%s, len(t.%s))\n", f.Name, f.Type, f.Name)
			dst := fmt.Sprintf("res.%s[i]", f.Name)
			switch {
			case isPtr(et) && f.ElemGenerated && (f.ElemShared || !shared):
				fmt.Fprintf(&b, "\t\tfor i, v := range t.%s { if v != nil { %s = %s } }\n",
					f.Name, dst, cloneCall("v", f.ElemShared))
			case isPtr(et) && f.ElemGenerated:
				fmt.Fprintf(&b, "\t\tfor i, v := range t.%s {\n\t\t\tif v != nil {\n", f.Name)
				b.WriteString(memoPtr("\t\t\t\t", "v", dst, et, "v.Clone()"))
				b.WriteString("\t\t\t}\n\t\t}\n")
			case isPtr(et) && f.ElemKnownValue && shared:
				fmt.Fprintf(&b, "\t\tfor i, v := range t.%s {\n\t\t\tif v != nil {\n", f.Name)
				b.WriteString(memoPtr("\t\t\t\t", "v", dst, et,
					fmt.Sprintf("func() %s { _v := *v; return &_v }()", et)))
				b.WriteString("\t\t\t}\n\t\t}\n")
			case isPtr(et) && f.ElemKnownValue:
				fmt.Fprintf(&b, "\t\tfor i, v := range t.%s { if v != nil { _v := *v; res.%s[i] = &_v } }\n", f.Name, f.Name)
			case isPtr(et):
				fmt.Fprintf(&b, "\t\tfor i, v := range t.%s { res.%s[i] = %s }\n", f.Name, f.Name, genericClone("v"))
			case f.ElemGenerated:
				fmt.Fprintf(&b, "\t\tfor i := range t.%s { res.%s[i] = *%s }\n",
					f.Name, f.Name, cloneCall(fmt.Sprintf("t.%s[i]", f.Name), f.ElemShared))
			case elemNeedsCopy(f, et):
				// Reference-typed or opaque elements are deep-copied one by one.
				fmt.Fprintf(&b, "\t\tfor i, v := range t.%s { res.%s[i] = %s }\n", f.Name, f.Name, genericClone("v"))
			default:
				// Elements an assignment copies whole.
				fmt.Fprintf(&b, "\t\tcopy(res.%s, t.%s)\n", f.Name, f.Name)
			}
			b.WriteString("\t}\n")
		} else if strings.HasPrefix(f.Type, "map[") {
			vt := mapVal(f.Type)
			fmt.Fprintf(&b, "\tif t.%s != nil {\n\t\tres.%s = make(%s)\n", f.Name, f.Name, f.Type)
			fmt.Fprintf(&b, "\t\tfor k, v := range t.%s {\n", f.Name)
			dst := fmt.Sprintf("res.%s[k]", f.Name)
			switch {
			case isPtr(vt) && f.ElemGenerated && (f.ElemShared || !shared):
				fmt.Fprintf(&b, "\t\t\tif v != nil { %s = %s }\n", dst, cloneCall("v", f.ElemShared))
			case isPtr(vt) && f.ElemGenerated:
				b.WriteString("\t\t\tif v != nil {\n")
				b.WriteString(memoPtr("\t\t\t\t", "v", dst, vt, "v.Clone()"))
				b.WriteString("\t\t\t}\n")
			case isPtr(vt) && f.ElemKnownValue && shared:
				b.WriteString("\t\t\tif v != nil {\n")
				b.WriteString(memoPtr("\t\t\t\t", "v", dst, vt,
					fmt.Sprintf("func() %s { _v := *v; return &_v }()", vt)))
				b.WriteString("\t\t\t}\n")
			case isPtr(vt) && f.ElemKnownValue:
				fmt.Fprintf(&b, "\t\t\tif v != nil { _v := *v; res.%s[k] = &_v }\n", f.Name)
			case isPtr(vt):
				fmt.Fprintf(&b, "\t\t\tres.%s[k] = %s\n", f.Name, genericClone("v"))
			case f.ElemGenerated:
				if f.ElemShared {
					// See equalFieldCode: a map value has no stable address, so
					// it is copied into a variable declared inside the loop
					// body before the memo keys on it.
					b.WriteString("\t\t\t_e := v\n")
					fmt.Fprintf(&b, "\t\t\tres.%s[k] = *%s\n", f.Name, cloneCall("_e", true))
				} else {
					fmt.Fprintf(&b, "\t\t\tres.%s[k] = *v.Clone()\n", f.Name)
				}
			case elemNeedsCopy(f, vt):
				// Reference-typed or opaque values are deep-copied one by one.
				fmt.Fprintf(&b, "\t\t\tres.%s[k] = %s\n", f.Name, genericClone("v"))
			default:
				fmt.Fprintf(&b, "\t\t\tres.%s[k] = v\n", f.Name)
			}
			b.WriteString("\t\t}\n\t}\n")
		}
	}
	return b.String()
}

// ── templates ────────────────────────────────────────────────────────────────

var tmplFuncs = template.FuncMap{
	"fieldApplyCase": fieldApplyCase,
	"delegateCase":   delegateCase,
	"diffFieldCode":  diffFieldCode,
	"evalCondCase":   evalCondCase,
	"equalFieldCode": equalFieldCode,
	"copyFieldInit":  copyFieldInit,
	"copyFieldPost":  copyFieldPost,
	"not":            func(b bool) bool { return !b },
}

var headerTmpl = template.Must(template.New("header").Funcs(tmplFuncs).Parse(
	`// Code generated by deep-gen. DO NOT EDIT.
package {{.PkgName}}

import (
	"fmt"
	"log/slog"
{{- if .NeedsReflect}}
	"reflect"
{{- end}}
{{- if .NeedsRegexp}}
	"regexp"
{{- end}}
{{- if .NeedsStrings}}
	"strings"
{{- end}}
{{- if .NeedsCondition}}
	"github.com/brunoga/deep/v5/condition"
{{- end}}
{{- if .NeedsDeep}}
	deep "github.com/brunoga/deep/v5"
{{- end}}
{{- if .NeedsCrdt}}
	crdt "github.com/brunoga/deep/v5/crdt"
{{- end}}
{{- if .NeedsGen}}
	gen "github.com/brunoga/deep/v5/gen"
{{- end}}
{{- range .Extra}}
	{{if .Alias}}{{.Alias}} {{end}}"{{.Path}}"
{{- end}}
)
`))

var patchTmpl = template.Must(template.New("patch").Funcs(tmplFuncs).Parse(
	`// Patch applies p to t using the generated fast path.
func (t *{{.TypeName}}) Patch(p {{.P}}Patch[{{.TypeName}}], logger *slog.Logger) error {
	if logger == nil { logger = slog.Default() }
	if p.Guard != nil {
		ok, err := t.evaluateCondition(*p.Guard)
		if err != nil {
			return fmt.Errorf("global condition evaluation failed: %w", err)
		}
		if !ok {
			return fmt.Errorf("global condition not met")
		}
	}
	var errs []error
	for _, op := range p.Operations {
		op.Strict = p.Strict
		handled, err := t.applyOperation(op, logger)
		if err != nil {
			errs = append(errs, err)
		} else if !handled {
			if err := {{.G}}ApplyOpReflection(t, op, logger); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if len(errs) > 0 {
		return &{{.P}}ApplyError{Errors: errs}
	}
	return nil
}

`))

var applyOpTmpl = template.Must(template.New("applyOp").Funcs(tmplFuncs).Parse(
	`func (t *{{.TypeName}}) applyOperation(op {{.P}}Operation, logger *slog.Logger) (bool, error) {
	if op.If != nil {
		ok, err := t.evaluateCondition(*op.If)
		if err != nil { return true, fmt.Errorf("condition evaluation failed at %s: %w", op.Path, err) }
		if !ok { return true, nil }
	}
	if op.Unless != nil {
		ok, err := t.evaluateCondition(*op.Unless)
		if err != nil { return true, fmt.Errorf("condition evaluation failed at %s: %w", op.Path, err) }
		if ok { return true, nil }
	}
	if op.Kind == {{.P}}OpLog {
		logger.Info("deep log", "message", op.New, "path", op.Path)
		return true, nil
	}

	switch op.Path {
	case "/":
		if op.Strict && op.Old != nil && (op.Kind == {{.P}}OpReplace || op.Kind == {{.P}}OpRemove) {
			_match := false
			if old, ok := op.Old.({{.TypeName}}); ok {
				_match = {{.P}}Equal(*t, old)
			}
			if !_match {
				_match = {{.P}}EqualCoerced(*t, op.Old)
			}
			if !_match {
				return true, fmt.Errorf("strict check failed at root: expected %v, got %v", op.Old, *t)
			}
		}
		if op.Kind == {{.P}}OpReplace {
			if v, ok := op.New.({{.TypeName}}); ok {
				*t = v
				return true, nil
			}
		}
		return true, fmt.Errorf("unsupported root operation: %s", op.Kind)
{{range .Fields}}{{if .HasApplyCase}}{{fieldApplyCase . $.P}}{{end}}{{end -}}
	default:
{{range .Fields}}{{delegateCase . $.P}}{{end -}}
	}
	return false, nil
}

`))

var diffTmpl = template.Must(template.New("diff").Funcs(tmplFuncs).Parse(
	`{{if .Shared}}// Diff compares t with other and returns a Patch.
//
// {{.TypeName}} values can hold two references to the same value, so a pair of
// values is diffed once, at the first path that reaches it. Every later route
// to a changed pair becomes one alias operation — "make this path hold the
// object that path holds" — appended after the rest. That keeps the patch
// linear where listing every route could be exponential, and applying it
// rebuilds the sharing whatever the target looked like before.
func (t *{{.TypeName}}) Diff(other *{{.TypeName}}) {{.P}}Patch[{{.TypeName}}] {
	seen := {{.G}}NewDiffMemo()
	defer seen.Release()
	p := t.diffShared(other, seen, "")
	p.Operations = append(p.Operations, seen.AliasOperations()...)
	return p
}

// diffShared is Diff threaded with the pairs already visited and the absolute
// path at which t sits.
func (t *{{.TypeName}}) diffShared(other *{{.TypeName}}, seen *{{.G}}DiffMemo, at string) {{.P}}Patch[{{.TypeName}}] {
	p := {{.P}}Patch[{{.TypeName}}]{}
	if t == other || !seen.Enter(t, other, at) {
		return p
	}
{{range .Fields}}{{diffFieldCode . $.P $.G $.TypeKeys $.Shared}}{{end}}
	seen.Leave(t, other, len(p.Operations))
	return p
}

{{else}}// Diff compares t with other and returns a Patch.
func (t *{{.TypeName}}) Diff(other *{{.TypeName}}) {{.P}}Patch[{{.TypeName}}] {
	p := {{.P}}Patch[{{.TypeName}}]{}
{{range .Fields}}{{diffFieldCode . $.P $.G $.TypeKeys $.Shared}}{{end}}
	return p
}

{{end}}`))

var evalCondTmpl = template.Must(template.New("evalCond").Funcs(tmplFuncs).Parse(
	`func (t *{{.TypeName}}) evaluateCondition(c condition.Condition) (bool, error) {
	switch c.Op {
	case "and":
		for _, sub := range c.Sub {
			ok, err := t.evaluateCondition(*sub)
			if err != nil || !ok { return false, err }
		}
		return true, nil
	case "or":
		for _, sub := range c.Sub {
			ok, err := t.evaluateCondition(*sub)
			if err == nil && ok { return true, nil }
		}
		return false, nil
	case "not":
		if len(c.Sub) > 0 {
			ok, err := t.evaluateCondition(*c.Sub[0])
			if err != nil { return false, err }
			return !ok, nil
		}
		return true, nil
	}

	switch c.Path {
{{range .Fields}}{{if .HasCondCase -}}
	{{if ne .JSONName .Name}}case "/{{.JSONName}}", "/{{.Name}}":{{else}}case "/{{.Name}}":{{end}}
{{evalCondCase . $.P}}{{end}}{{end -}}
	}
	// Anything the fast path does not model — nested paths, collection
	// lookups, unusual ops — is evaluated by the reflection engine, which
	// handles the full condition language.
	return condition.Evaluate(reflect.ValueOf(t).Elem(), &c)
}

`))

var equalTmpl = template.Must(template.New("equal").Funcs(tmplFuncs).Parse(
	`{{if .Shared}}// Equal returns true if t and other are deeply equal.
//
// {{.TypeName}} values can hold two references to the same value, so a pair of
// values is compared once however many routes lead to it, and a cycle is
// followed only until it repeats.
func (t *{{.TypeName}}) Equal(other *{{.TypeName}}) bool {
	seen := {{.G}}NewVisitSet()
	defer seen.Release()
	return t.equalShared(other, seen)
}

// equalShared is Equal threaded with the set of pairs already under comparison.
func (t *{{.TypeName}}) equalShared(other *{{.TypeName}}, seen *{{.G}}VisitSet) bool {
	if t == other {
		return true
	}
	if t == nil || other == nil {
		return false
	}
	if !seen.Enter(t, other) {
		// This pair has been reached before: settled if that comparison
		// finished, and a cycle if it is still running — either way there is
		// nothing left to compare here.
		return true
	}
{{else}}// Equal returns true if t and other are deeply equal.
func (t *{{.TypeName}}) Equal(other *{{.TypeName}}) bool {
{{end}}{{range .Fields}}{{if not .Ignore}}{{equalFieldCode . $.P}}{{end}}{{end -}}
	return true
}

`))

var copyTmpl = template.Must(template.New("copy").Funcs(tmplFuncs).Parse(
	`{{if .Shared}}// Clone returns a deep copy of t.
//
// {{.TypeName}} values can hold two references to the same value — through a
// cycle, or through two routes to one pointer — so the copy keeps track of what
// it has already copied: a value reached more than once is copied once, every
// reference to it in the result points at that one copy, and a reference cycle
// is rebuilt rather than followed forever.
func (t *{{.TypeName}}) Clone() *{{.TypeName}} {
	memo := {{.G}}NewCloneMemo()
	defer memo.Release()
	return t.cloneShared(memo)
}

// cloneShared is Clone threaded with the memo of copies already made.
func (t *{{.TypeName}}) cloneShared(memo *{{.G}}CloneMemo) *{{.TypeName}} {
	if t == nil {
		return nil
	}
	if done, ok := memo.Load(t); ok {
		return done.(*{{.TypeName}})
	}
{{else}}// Clone returns a deep copy of t.
func (t *{{.TypeName}}) Clone() *{{.TypeName}} {
{{end}}	res := &{{.TypeName}}{
{{range .Fields}}{{if not .Ignore}}{{copyFieldInit .}}{{end}}{{end -}}
	}
{{if .Shared}}	// Recorded before the fields are copied, so a reference back to t from
	// anywhere below resolves to res instead of starting the copy again.
	memo.Store(t, res)
{{end}}{{range .Fields}}{{if not .Ignore}}{{copyFieldPost . $.P $.G $.Shared}}{{end}}{{end -}}
	return res
}
`))

// ── generator ────────────────────────────────────────────────────────────────

// writeHeader emits the package clause and import block for body. Which
// imports are needed is read back off the generated code itself, so a field
// that ends up handled by some other path can never leave an unused import
// behind.
func (g *Generator) writeHeader(body string) {
	must(headerTmpl.Execute(&g.buf, headerData{
		PkgName:        g.pkgName,
		NeedsReflect:   usesQualifier(body, "reflect"),
		NeedsRegexp:    usesQualifier(body, "regexp"),
		NeedsStrings:   usesQualifier(body, "strings"),
		NeedsCondition: usesQualifier(body, "condition"),
		NeedsDeep:      g.pkgName != "deep" && usesQualifier(body, "deep"),
		NeedsCrdt:      g.pkgName != "deep" && usesQualifier(body, "crdt"),
		NeedsGen:       usesQualifier(body, "gen"),
		Extra:          g.extraImports(body),
	}))
}

// extraImports returns the imports body needs for field types that refer to
// other packages.
func (g *Generator) extraImports(body string) []importSpec {
	quals := make([]string, 0, len(g.imports))
	for qual := range g.imports {
		quals = append(quals, qual)
	}
	sort.Strings(quals)

	var extra []importSpec
	for _, qual := range quals {
		path := g.imports[qual]
		if _, ok := fixedImports[qual]; ok {
			// The header template binds this qualifier itself. Qualifiers that
			// clash with it never reach here: renderType refuses to name those
			// types at all.
			continue
		}
		if !usesQualifier(body, qual) {
			continue
		}
		spec := importSpec{Path: path}
		if importBase(path) != qual {
			spec.Alias = qual
		}
		extra = append(extra, spec)
	}
	return extra
}

// fixedImports are the imports the header template emits on its own, keyed by
// the qualifier they bind.
var fixedImports = map[string]string{
	"fmt":       "fmt",
	"slog":      "log/slog",
	"reflect":   "reflect",
	"regexp":    "regexp",
	"strings":   "strings",
	"condition": "github.com/brunoga/deep/v5/condition",
	"deep":      deepPkgPath,
	"crdt":      crdtPkgPath,
}

const (
	deepPkgPath = "github.com/brunoga/deep/v5"
	crdtPkgPath = "github.com/brunoga/deep/v5/crdt"
)

var qualifierRes = map[string]*regexp.Regexp{}

// usesQualifier reports whether src refers to package qual.
func usesQualifier(src, qual string) bool {
	re, ok := qualifierRes[qual]
	if !ok {
		re = regexp.MustCompile(`(^|[^\w.])` + regexp.QuoteMeta(qual) + `\.`)
		qualifierRes[qual] = re
	}
	return re.MatchString(src)
}

func (g *Generator) writeType(typeName string, fields []FieldInfo) {
	if g.pkgName != "deep" {
		g.pkgPrefix = "deep."
	}
	// The bookkeeping types live in their own package, which the deep package
	// itself also imports rather than declaring.
	g.genPrefix = "gen."
	if g.pkgName == "gen" {
		g.genPrefix = ""
	}
	d := typeData{
		TypeName: typeName,
		P:        g.pkgPrefix,
		G:        g.genPrefix,
		Fields:   fields,
		TypeKeys: g.typeKeys,
		Shared:   g.idx.shared[typeName],
	}
	must(patchTmpl.Execute(&g.body, d))
	must(applyOpTmpl.Execute(&g.body, d))
	must(diffTmpl.Execute(&g.body, d))
	must(evalCondTmpl.Execute(&g.body, d))
	must(equalTmpl.Execute(&g.body, d))
	must(copyTmpl.Execute(&g.body, d))
}

func must(err error) {
	if err != nil {
		log.Fatalf("template error: %v", err)
	}
}

// ── AST parsing ──────────────────────────────────────────────────────────────

func main() {
	flag.Parse()
	if len(*typeNames) == 0 {
		log.Fatal("type flag required")
	}

	dir := "."
	if len(flag.Args()) > 0 {
		dir = flag.Args()[0]
	}

	// Determine output file: explicit -output flag, or default to
	// "{first_type_lowercase}_deep.go" in the target directory (like stringer).
	outFile := *outputFile
	if outFile == "" {
		firstName := strings.ToLower(strings.SplitN(*typeNames, ",", 2)[0])
		outFile = filepath.Join(dir, firstName+"_deep.go")
	}

	// Never read back the file this run is about to write: a stale copy has
	// nothing to contribute, and one left behind by an older version of the
	// generator could fail to parse and block regeneration entirely.
	var skip string
	if filepath.Clean(filepath.Dir(outFile)) == filepath.Clean(dir) {
		skip = filepath.Base(outFile)
	}

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi fs.FileInfo) bool {
		return skip == "" || fi.Name() != skip
	}, 0)
	if err != nil {
		log.Fatal(err)
	}

	var g *Generator

	for pkgName, pkg := range pkgs {
		if strings.HasSuffix(pkgName, "_test") {
			continue
		}
		if g == nil {
			g = &Generator{
				pkgName:  pkgName,
				typeKeys: make(map[string]string),
				idx: pkgIndex{
					structs: make(map[string]bool),
					scalars: make(map[string]bool),
					shared:  make(map[string]bool),
				},
				imports: make(map[string]string),
			}
		}

		requested := make(map[string]bool)
		for _, t := range strings.Split(*typeNames, ",") {
			requested[strings.TrimSpace(t)] = true
		}

		// Pass 1: index the package's own types and collect deep:"key" field
		// names. The struct bodies and their files' imports are kept so the
		// reference graph can be built once every declared name is known.
		structDecls := make(map[string]*ast.StructType)
		structImports := make(map[string]map[string]string)
		for _, file := range pkg.Files {
			imports := fileImports(file)
			ast.Inspect(file, func(n ast.Node) bool {
				ts, ok := n.(*ast.TypeSpec)
				if !ok {
					return true
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					if ident, ok := ts.Type.(*ast.Ident); ok && isBuiltinComparable(ident.Name) {
						g.idx.scalars[ts.Name.Name] = true
					}
					return true
				}
				g.idx.structs[ts.Name.Name] = true
				structDecls[ts.Name.Name] = st
				structImports[ts.Name.Name] = imports
				for _, field := range st.Fields.List {
					if field.Tag == nil || len(field.Names) == 0 {
						continue
					}
					tag := strings.Trim(field.Tag.Value, "`")
					if strings.Contains(tag, "deep:\"key\"") {
						g.typeKeys[ts.Name.Name] = field.Names[0].Name
					}
				}
				return true
			})
		}

		// With every declared struct known, work out which of them can hold two
		// references to the same value. Their generated methods track what they
		// have already visited.
		refs := make(map[string]map[string]bool, len(structDecls))
		for name, st := range structDecls {
			refs[name] = referencedStructs(st, g.idx.structs)
		}
		g.idx.shared = sharedStructs(structDecls, structImports, g.idx, refs)

		// Pass 2: collect field info for requested types.
		var allTypes []string
		var allFields [][]FieldInfo

		for _, file := range pkg.Files {
			imports := fileImports(file)
			ast.Inspect(file, func(n ast.Node) bool {
				ts, ok := n.(*ast.TypeSpec)
				if !ok || !requested[ts.Name.Name] {
					return true
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					return true
				}
				fields := parseFields(st, imports, g.idx)
				for _, f := range fields {
					for _, qual := range f.Quals {
						if prev, ok := g.imports[qual]; ok && prev != imports[qual] {
							log.Printf("deep-gen: package qualifier %q means both %q and %q in this package; using %q",
								qual, prev, imports[qual], prev)
							continue
						}
						g.imports[qual] = imports[qual]
					}
				}
				allTypes = append(allTypes, ts.Name.Name)
				allFields = append(allFields, fields)
				return false
			})
		}

		if len(allTypes) == 0 {
			continue
		}

		for i := range allTypes {
			g.writeType(allTypes[i], allFields[i])
		}
		// The header comes last: it is derived from the generated body.
		g.writeHeader(g.body.String())
		g.buf.Write(g.body.Bytes())
		g.body.Reset()
	}

	if g == nil {
		return
	}

	src, err := format.Source(g.buf.Bytes())
	if err != nil {
		log.Printf("warning: gofmt failed: %v", err)
		src = g.buf.Bytes()
	}

	if err := os.WriteFile(outFile, src, 0644); err != nil {
		log.Fatalf("writing output: %v", err)
	}
	log.Printf("deep-gen: wrote %s", outFile)
}

func parseFields(st *ast.StructType, imports map[string]string, idx pkgIndex) []FieldInfo {
	var fields []FieldInfo
	for _, field := range st.Fields.List {
		// An embedded field is addressed by its type name, the same way the
		// reflection engine (and the language) names it.
		names := make([]string, 0, len(field.Names))
		for _, ident := range field.Names {
			names = append(names, ident.Name)
		}
		if len(names) == 0 {
			name := embeddedFieldName(field.Type)
			if name == "" {
				log.Printf("deep-gen: embedded field with an unsupported type; it will be handled by the reflection fallback")
				continue
			}
			names = []string{name}
		}
		var ignore, readOnly, atomic bool
		// Tags apply to all names in the declaration (e.g. `X, Y int \`json:"x"\``
		// is unusual but syntactically valid; we honour the tag for every name).
		if field.Tag != nil {
			tagVal := strings.Trim(field.Tag.Value, "`")
			tag := reflect.StructTag(tagVal)
			// json:"-" marks the whole field as ignored
			if jt := tag.Get("json"); strings.Split(jt, ",")[0] == "-" {
				ignore = true
			}
			for _, p := range strings.Split(tag.Get("deep"), ",") {
				switch strings.TrimSpace(p) {
				case "-":
					ignore = true
				case "readonly":
					readOnly = true
				case "atomic":
					atomic = true
				}
			}
		}

		ti := resolveType(field.Type, imports, idx)
		for _, name := range names {
			jsonName := name
			if field.Tag != nil {
				tagVal := strings.Trim(field.Tag.Value, "`")
				tag := reflect.StructTag(tagVal)
				if jt := tag.Get("json"); jt != "" {
					if part := strings.Split(jt, ",")[0]; part != "" && part != "-" {
						jsonName = part
					}
				}
			}
			if ti.name == "" && !ignore {
				log.Printf("deep-gen: field %s has an unsupported type; it will be handled by the reflection fallback", name)
			}
			fields = append(fields, FieldInfo{
				Name:           name,
				JSONName:       jsonName,
				Type:           ti.name,
				IsStruct:       ti.isStruct,
				IsCollection:   ti.isCollection,
				IsText:         ti.isText,
				ElemGenerated:  ti.elemGenerated,
				TypeShared:     ti.shared,
				ElemShared:     ti.elemShared,
				Comparable:     ti.comparable,
				ElemComparable: ti.elemComparable,
				KnownValue:     ti.knownValue,
				ElemKnownValue: ti.elemKnownValue,
				PointeeKnown:   ti.pointeeKnown,
				Quals:          ti.quals,
				Ignore:         ignore,
				ReadOnly:       readOnly,
				Atomic:         atomic,
			})
		}
	}
	return fields
}

// pkgIndex records what the generator knows about the types declared in the
// package it is generating for.
type pkgIndex struct {
	// structs are the struct types: they have the generated methods, so field
	// code can call Equal/Clone/applyOperation on them.
	structs map[string]bool
	// scalars are named types over a comparable builtin (type Priority int),
	// for which == is value equality.
	scalars map[string]bool
	// shared are the structs whose values can hold two references to the same
	// value — through a cycle, or simply through two routes to one pointer.
	// Their generated methods carry a memo (Clone), a visited set (Equal) or a
	// diff memo (Diff); without one, a shared value would be duplicated, a
	// cycle would recurse until the stack ran out, and a diff could be
	// exponential. Structs that cannot hold two references to anything generate
	// exactly the code they always did.
	shared map[string]bool
}

// referencedStructs returns the package structs named anywhere in st's field
// types. Qualified names are skipped: a type from another package is opaque
// here, so it cannot be an edge in this package's reference graph.
func referencedStructs(st *ast.StructType, structs map[string]bool) map[string]bool {
	out := make(map[string]bool)
	for _, field := range st.Fields.List {
		ast.Inspect(field.Type, func(n ast.Node) bool {
			if _, ok := n.(*ast.SelectorExpr); ok {
				return false
			}
			if id, ok := n.(*ast.Ident); ok && structs[id.Name] {
				out[id.Name] = true
			}
			return true
		})
	}
	return out
}

// classRoutes returns, for one struct, how many routes lead to each kind of
// shareable reference — capped at two, since "more than one" is all that
// matters. Classes are rendered pointer types ("*time.Time", "*Meta"); the
// pseudo-class "?" stands for opaque values (interfaces, foreign structs,
// unnameable types) that may hold references the generator cannot see, and so
// can alias with anything.
//
// Reaching a collection counts its element routes twice: two elements of one
// slice can point at the same value. Reaching a package struct adds that
// struct's own routes. Ignored fields are counted anyway — over-counting can
// only cost a memo, never correctness.
func classRoutes(name string, decls map[string]*ast.StructType,
	imps map[string]map[string]string, idx pkgIndex,
	memo map[string]map[string]int, active map[string]bool) map[string]int {

	if done, ok := memo[name]; ok {
		return done
	}
	if active[name] {
		// A cycle in the expansion; the cyclic rule already forces the memo for
		// everything on it, so the partial answer is fine here.
		return nil
	}
	active[name] = true
	defer delete(active, name)

	add := func(dst map[string]int, class string, n int) {
		if v := dst[class] + n; v > 2 {
			dst[class] = 2
		} else {
			dst[class] = v
		}
	}
	merge := func(dst, src map[string]int, factor int) {
		for c, n := range src {
			add(dst, c, n*factor)
		}
	}

	routes := make(map[string]int)
	st := decls[name]
	imports := imps[name]
	for _, field := range st.Fields.List {
		ti := resolveType(field.Type, imports, idx)
		switch {
		case ti.name == "":
			add(routes, "?", 1)
		case ti.isText, ti.comparable, ti.knownValue:
			// Value semantics: nothing to share.
		case ti.isStruct && isPtr(ti.name):
			add(routes, ti.name, 1)
			merge(routes, classRoutes(ti.name[1:], decls, imps, idx, memo, active), 1)
		case ti.isStruct:
			merge(routes, classRoutes(ti.name, decls, imps, idx, memo, active), 1)
		case ti.isCollection:
			et := mapVal(ti.name)
			if strings.HasPrefix(ti.name, "[]") {
				et = sliceElem(ti.name)
			}
			switch {
			case isPtr(et):
				add(routes, et, 2)
				if ti.elemGenerated {
					merge(routes, classRoutes(et[1:], decls, imps, idx, memo, active), 2)
				}
			case ti.elemGenerated:
				merge(routes, classRoutes(et, decls, imps, idx, memo, active), 2)
			case ti.elemComparable, ti.elemKnownValue:
				// Value elements: nothing to share.
			default:
				add(routes, "?", 2)
			}
		case isPtr(ti.name):
			// Foreign pointer: the pointer itself is the shareable identity.
			add(routes, ti.name, 1)
		default:
			// Opaque value — may hold references the generator cannot see.
			add(routes, "?", 1)
		}
	}
	memo[name] = routes
	return routes
}

// sharedStructs returns the structs whose generated methods must track what
// they have already visited: those on or reaching a cycle, and those where two
// routes can lead to one reference. The set is then closed downward — a shared
// struct's non-shared struct fields must thread the memo too when they hold
// references of their own, or the sharing between a reference inside them and
// one outside would be lost.
func sharedStructs(decls map[string]*ast.StructType, imps map[string]map[string]string,
	idx pkgIndex, refs map[string]map[string]bool) map[string]bool {

	out := cyclicStructs(refs)

	memo := make(map[string]map[string]int, len(decls))
	for name := range decls {
		routes := classRoutes(name, decls, imps, idx, memo, make(map[string]bool))
		if aliasPossible(routes) {
			out[name] = true
		}
	}

	// Downward closure: every struct a shared struct reaches that has routes of
	// its own becomes shared, so the memo travels with the value all the way
	// down. Structs with no routes stay plain — they have nothing to record.
	changed := true
	for changed {
		changed = false
		for name := range out {
			for ref := range refs[name] {
				if !out[ref] && len(memo[ref]) > 0 {
					out[ref] = true
					changed = true
				}
			}
		}
	}
	return out
}

// aliasPossible reports whether two routes can lead to the same reference: a
// specific class reachable twice, or an opaque value ("?" — which can hold
// anything) next to any other route.
func aliasPossible(routes map[string]int) bool {
	for class, n := range routes {
		if n >= 2 {
			return true
		}
		if class == "?" && len(routes) >= 2 {
			return true
		}
	}
	return false
}

// cyclicStructs returns the structs whose generated methods need cycle
// handling: those that lie on a cycle in the reference graph, plus those that
// can reach one. A type that reaches a recursive type has to thread the memo
// down to it, or the recursion below would be unguarded.
//
// The graph is small — the structs of one package — so this walks it directly
// rather than condensing it into components.
func cyclicStructs(refs map[string]map[string]bool) map[string]bool {
	// reach[a][b] reports whether a reaches b over one or more edges, so
	// reach[a][a] means a lies on a cycle.
	reach := make(map[string]map[string]bool, len(refs))
	for name := range refs {
		seen := make(map[string]bool)
		var stack []string
		for next := range refs[name] {
			stack = append(stack, next)
		}
		for len(stack) > 0 {
			cur := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if seen[cur] {
				continue
			}
			seen[cur] = true
			for next := range refs[cur] {
				stack = append(stack, next)
			}
		}
		reach[name] = seen
	}

	out := make(map[string]bool)
	for name := range refs {
		if reach[name][name] {
			out[name] = true
			continue
		}
		for other := range reach[name] {
			if reach[other][other] {
				out[name] = true
				break
			}
		}
	}
	return out
}

// typeInfo is what the generator managed to work out about a field's type.
type typeInfo struct {
	name           string
	isStruct       bool
	isCollection   bool
	isText         bool
	elemGenerated  bool
	shared         bool
	elemShared     bool
	comparable     bool
	elemComparable bool
	knownValue     bool
	elemKnownValue bool
	pointeeKnown   bool
	quals          []string
}

// resolveType classifies a field type. imports maps the package qualifiers
// visible in the file to their import paths.
func resolveType(expr ast.Expr, imports map[string]string, idx pkgIndex) typeInfo {
	expr = unparen(expr)

	quals := make(map[string]bool)
	name, ok := renderType(expr, imports, quals)
	if !ok {
		// Nothing else can be trusted about a type we cannot even name.
		return typeInfo{}
	}
	ti := typeInfo{name: name}

	if isTextType(expr, imports) {
		// Text is a CRDT value with its own merge semantics; it is always
		// referred to as crdt.Text in generated code, whatever the source file
		// called the package.
		return typeInfo{name: "crdt.Text", isText: true}
	}
	for q := range quals {
		ti.quals = append(ti.quals, q)
	}
	sort.Strings(ti.quals)

	switch typ := expr.(type) {
	case *ast.Ident:
		ti.isStruct = isGeneratedStruct(typ, idx)
		ti.shared = idx.shared[generatedStructName(typ, idx)]
		ti.comparable = idx.isComparable(typ)
	case *ast.SelectorExpr:
		ti.knownValue = isKnownValueExpr(typ, imports)
	case *ast.StarExpr:
		ti.isStruct = isGeneratedStruct(unparen(typ.X), idx)
		ti.shared = idx.shared[generatedStructName(typ.X, idx)]
		ti.pointeeKnown = isKnownValueExpr(unparen(typ.X), imports)
	case *ast.ArrayType:
		// Fixed-size arrays are values, not collections: patching into them is
		// left to the reflection fallback.
		if typ.Len == nil {
			ti.isCollection = true
			ti.elemGenerated = isGeneratedStructRef(typ.Elt, idx)
			ti.elemShared = idx.shared[generatedStructName(typ.Elt, idx)]
			ti.elemComparable = idx.isComparable(unparen(typ.Elt))
			ti.elemKnownValue = isKnownValueRef(typ.Elt, imports)
		}
	case *ast.MapType:
		ti.isCollection = true
		ti.elemGenerated = isGeneratedStructRef(typ.Value, idx)
		ti.elemShared = idx.shared[generatedStructName(typ.Value, idx)]
		ti.elemComparable = idx.isComparable(unparen(typ.Value))
		ti.elemKnownValue = isKnownValueRef(typ.Value, imports)
	}
	return ti
}

// renderType renders expr as Go source and records every package qualifier it
// uses in quals. It reports false for types the generator cannot name, either
// because the syntax is not supported (funcs, channels, non-empty interfaces)
// or because a qualifier cannot be resolved to an import.
func renderType(expr ast.Expr, imports map[string]string, quals map[string]bool) (string, bool) {
	switch typ := unparen(expr).(type) {
	case *ast.Ident:
		return typ.Name, true
	case *ast.SelectorExpr:
		pkg, ok := typ.X.(*ast.Ident)
		if !ok {
			return "", false
		}
		path, known := imports[pkg.Name]
		if !known {
			return "", false
		}
		if fixed, ok := fixedImports[pkg.Name]; ok && fixed != path {
			// The generated file binds this qualifier to its own import, so
			// naming the type here would resolve to the wrong package. Leave
			// the field to the reflection fallback instead.
			return "", false
		}
		quals[pkg.Name] = true
		return pkg.Name + "." + typ.Sel.Name, true
	case *ast.StarExpr:
		inner, ok := renderType(typ.X, imports, quals)
		if !ok {
			return "", false
		}
		return "*" + inner, true
	case *ast.ArrayType:
		elem, ok := renderType(typ.Elt, imports, quals)
		if !ok {
			return "", false
		}
		if typ.Len == nil {
			return "[]" + elem, true
		}
		lit, ok := typ.Len.(*ast.BasicLit)
		if !ok {
			return "", false // array length is a constant expression we cannot render
		}
		return "[" + lit.Value + "]" + elem, true
	case *ast.MapType:
		key, ok := renderType(typ.Key, imports, quals)
		if !ok {
			return "", false
		}
		val, ok := renderType(typ.Value, imports, quals)
		if !ok {
			return "", false
		}
		return "map[" + key + "]" + val, true
	case *ast.InterfaceType:
		if typ.Methods == nil || len(typ.Methods.List) == 0 {
			return "any", true
		}
		return "", false
	case *ast.IndexExpr: // generic instantiation with one type argument
		return renderIndex(typ.X, []ast.Expr{typ.Index}, imports, quals)
	case *ast.IndexListExpr: // generic instantiation with several type arguments
		return renderIndex(typ.X, typ.Indices, imports, quals)
	}
	return "", false
}

func renderIndex(x ast.Expr, indices []ast.Expr, imports map[string]string, quals map[string]bool) (string, bool) {
	base, ok := renderType(x, imports, quals)
	if !ok {
		return "", false
	}
	args := make([]string, 0, len(indices))
	for _, idx := range indices {
		arg, ok := renderType(idx, imports, quals)
		if !ok {
			return "", false
		}
		args = append(args, arg)
	}
	return base + "[" + strings.Join(args, ", ") + "]", true
}

func unparen(expr ast.Expr) ast.Expr {
	for {
		paren, ok := expr.(*ast.ParenExpr)
		if !ok {
			return expr
		}
		expr = paren.X
	}
}

// isKnownValueExpr reports whether expr names a stdlib type known to have
// value semantics: copying it by assignment is a full copy. The list is
// deliberately tiny — only types this can be stated about with certainty.
// Resolution goes through the file's imports, so an aliased stdlib time
// package still qualifies and a user package that happens to be called "time"
// does not.
func isKnownValueExpr(expr ast.Expr, imports map[string]string) bool {
	sel, ok := unparen(expr).(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || imports[pkg.Name] != "time" {
		return false
	}
	switch sel.Sel.Name {
	case "Time", "Duration", "Month", "Weekday":
		return true
	}
	return false
}

// isKnownValueRef is isKnownValueExpr, looking through one pointer.
func isKnownValueRef(expr ast.Expr, imports map[string]string) bool {
	expr = unparen(expr)
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = unparen(star.X)
	}
	return isKnownValueExpr(expr, imports)
}

// isTextType reports whether expr refers to crdt.Text.
func isTextType(expr ast.Expr, imports map[string]string) bool {
	switch typ := expr.(type) {
	case *ast.Ident:
		return typ.Name == "Text"
	case *ast.SelectorExpr:
		pkg, ok := typ.X.(*ast.Ident)
		return ok && typ.Sel.Name == "Text" && imports[pkg.Name] == crdtPkgPath
	}
	return false
}

// isGeneratedStruct reports whether expr names an exported struct declared in
// the package being generated, and therefore has the generated methods.
// generatedStructName returns the name of the package struct expr denotes,
// looking through one pointer. It returns "" for anything else, which never
// matches an entry in idx.shared.
func generatedStructName(expr ast.Expr, idx pkgIndex) string {
	expr = unparen(expr)
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = unparen(star.X)
	}
	ident, ok := expr.(*ast.Ident)
	if !ok || !idx.structs[ident.Name] {
		return ""
	}
	return ident.Name
}

func isGeneratedStruct(expr ast.Expr, idx pkgIndex) bool {
	ident, ok := expr.(*ast.Ident)
	if !ok || !idx.structs[ident.Name] {
		return false
	}
	return ident.Name[0] >= 'A' && ident.Name[0] <= 'Z'
}

// isGeneratedStructRef is isGeneratedStruct, looking through one pointer.
func isGeneratedStructRef(expr ast.Expr, idx pkgIndex) bool {
	expr = unparen(expr)
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = unparen(star.X)
	}
	return isGeneratedStruct(expr, idx)
}

// isComparable reports whether == compares values of expr's type by value.
func (idx pkgIndex) isComparable(expr ast.Expr) bool {
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return false
	}
	return isBuiltinComparable(ident.Name) || idx.scalars[ident.Name]
}

// embeddedFieldName returns the implicit field name of an embedded field: the
// bare type name, looking through pointers, qualifiers and type arguments.
func embeddedFieldName(expr ast.Expr) string {
	switch typ := unparen(expr).(type) {
	case *ast.Ident:
		return typ.Name
	case *ast.SelectorExpr:
		return typ.Sel.Name
	case *ast.StarExpr:
		return embeddedFieldName(typ.X)
	case *ast.IndexExpr:
		return embeddedFieldName(typ.X)
	case *ast.IndexListExpr:
		return embeddedFieldName(typ.X)
	}
	return ""
}

// fileImports maps the package qualifiers usable in file to their import paths.
// Blank and dot imports are skipped: neither introduces a usable qualifier.
func fileImports(file *ast.File) map[string]string {
	m := make(map[string]string, len(file.Imports))
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		name := importBase(path)
		if imp.Name != nil {
			if imp.Name.Name == "_" || imp.Name.Name == "." {
				continue
			}
			name = imp.Name.Name
		}
		m[name] = path
	}
	return m
}

// importBase guesses the package name of an import path. Go tooling proper
// reads it from the package clause; without type information the last path
// element (ignoring a major-version suffix) is the standard approximation.
func importBase(path string) string {
	elems := strings.Split(path, "/")
	base := elems[len(elems)-1]
	if len(elems) > 1 && isVersionElem(base) {
		base = elems[len(elems)-2]
	}
	// gopkg.in/yaml.v3 -> yaml
	if i := strings.LastIndex(base, "."); i > 0 && isVersionElem(base[i+1:]) {
		base = base[:i]
	}
	return base
}

func isVersionElem(s string) bool {
	if len(s) < 2 || s[0] != 'v' {
		return false
	}
	for _, r := range s[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
