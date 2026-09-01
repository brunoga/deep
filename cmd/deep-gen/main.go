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
	// Comparable is true when == compares the field by value: a builtin
	// comparable type, or a named type over one.
	Comparable bool
	// ElemComparable is Comparable for a collection's element or value type.
	ElemComparable bool
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
	buf       bytes.Buffer
	body      bytes.Buffer      // everything below the import block
	typeKeys  map[string]string // typeName -> keyFieldName (from deep:"key" tag)
	idx       pkgIndex          // types declared in this package
	imports   map[string]string // package qualifier -> import path, from the parsed sources
}

// ── template data structs ────────────────────────────────────────────────────

type headerData struct {
	PkgName        string
	NeedsRegexp    bool
	NeedsStrings   bool
	NeedsCondition bool
	NeedsDeep      bool
	NeedsCrdt      bool
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
	P        string // package prefix
	Fields   []FieldInfo
	TypeKeys map[string]string
}

// ── helpers used by both templates and FuncMap ───────────────────────────────

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
	return isPtr(t) || f.ElemGenerated || isReferenceType(t)
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
	// OpLog
	fmt.Fprintf(&b, "\t\tif op.Kind == %sOpLog {\n", p)
	fmt.Fprintf(&b, "\t\t\tlogger.Info(\"deep log\", \"message\", op.New, \"path\", op.Path, \"field\", t.%s)\n", f.Name)
	b.WriteString("\t\t\treturn true, nil\n\t\t}\n")
	// Strict check
	fmt.Fprintf(&b, "\t\tif op.Kind == %sOpReplace && op.Strict {\n", p)
	if f.IsStruct || f.IsText || f.IsCollection || f.Opaque() {
		fmt.Fprintf(&b, "\t\t\tif old, ok := op.Old.(%s); !ok || !%sEqual(t.%s, old) {\n", f.Type, p, f.Name)
		fmt.Fprintf(&b, "\t\t\t\treturn true, fmt.Errorf(\"strict check failed at %%s: expected %%v, got %%v\", op.Path, op.Old, t.%s)\n", f.Name)
		b.WriteString("\t\t\t}\n")
	} else if isNumericType(f.Type) {
		// Numeric types: op.Old may be float64 after JSON roundtrip.
		fmt.Fprintf(&b, "\t\t\t_oldOK := false\n")
		fmt.Fprintf(&b, "\t\t\tif _oldV, ok := op.Old.(%s); ok { _oldOK = t.%s == _oldV }\n", f.Type, f.Name)
		fmt.Fprintf(&b, "\t\t\tif !_oldOK { if _oldF, ok := op.Old.(float64); ok { _oldOK = %s(t.%s) == _oldF } }\n", "float64", f.Name)
		fmt.Fprintf(&b, "\t\t\tif !_oldOK {\n")
		fmt.Fprintf(&b, "\t\t\t\treturn true, fmt.Errorf(\"strict check failed at %%s: expected %%v, got %%v\", op.Path, op.Old, t.%s)\n", f.Name)
		b.WriteString("\t\t\t}\n")
	} else {
		fmt.Fprintf(&b, "\t\t\tif _oldV, ok := op.Old.(%s); !ok || t.%s != _oldV {\n", f.Type, f.Name)
		fmt.Fprintf(&b, "\t\t\t\treturn true, fmt.Errorf(\"strict check failed at %%s: expected %%v, got %%v\", op.Path, op.Old, t.%s)\n", f.Name)
		b.WriteString("\t\t\t}\n")
	}
	b.WriteString("\t\t}\n")
	// Value assignment
	if f.IsText {
		// Text is a convergent CRDT type — delegate via Patch with a single-op sub-patch.
		fmt.Fprintf(&b, "\t\top.Path = \"/\"\n")
		fmt.Fprintf(&b, "\t\treturn true, t.%s.Patch(%sPatch[crdt.Text]{Operations: []%sOperation{op}}, logger)\n", f.Name, p, p)
		return b.String()
	}
	fmt.Fprintf(&b, "\t\tif v, ok := op.New.(%s); ok {\n\t\t\tt.%s = v\n\t\t\treturn true, nil\n\t\t}\n", f.Type, f.Name)
	// Numeric float64 fallback (JSON deserialises numbers as float64)
	if f.Type == "int" || f.Type == "int64" || f.Type == "float64" {
		fmt.Fprintf(&b, "\t\tif f, ok := op.New.(float64); ok {\n\t\t\tt.%s = %s(f)\n\t\t\treturn true, nil\n\t\t}\n", f.Name, f.Type)
	}
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
			b.WriteString("\t\t\tkey := parts[0]\n")
			fmt.Fprintf(&b, "\t\t\tif val, ok := t.%s[key]; ok && val != nil {\n", f.Name)
			b.WriteString("\t\t\t\top.Path = \"/\"\n")
			b.WriteString("\t\t\t\tif len(parts) > 1 { op.Path = \"/\" + strings.Join(parts[1:], \"/\") }\n")
			b.WriteString("\t\t\t\treturn val.applyOperation(op, logger)\n\t\t\t}\n")
		} else {
			fmt.Fprintf(&b, "\t\t\tparts := strings.Split(op.Path[len(\"/%s/\"):], \"/\")\n", f.JSONName)
			b.WriteString("\t\t\tkey := parts[0]\n")
			fmt.Fprintf(&b, "\t\t\tif op.Kind == %sOpRemove {\n", p)
			fmt.Fprintf(&b, "\t\t\t\tdelete(t.%s, key)\n\t\t\t\treturn true, nil\n\t\t\t}\n", f.Name)
			fmt.Fprintf(&b, "\t\t\tif t.%s == nil { t.%s = make(%s) }\n", f.Name, f.Name, f.Type)
			fmt.Fprintf(&b, "\t\t\tif v, ok := op.New.(%s); ok {\n\t\t\t\tt.%s[key] = v\n\t\t\t\treturn true, nil\n\t\t\t}\n", vt, f.Name)
		}
		b.WriteString("\t\t}\n")
	}
	return b.String()
}

// diffFieldCode returns the diff fragment for one field.
func diffFieldCode(f FieldInfo, p string, typeKeys map[string]string) string {
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
		// Only pointer fields need a nil guard.
		needsGuard := isPtr(f.Type)
		if needsGuard {
			fmt.Fprintf(&b, "\tif %s != nil && %s != nil {\n", self, other)
		}
		fmt.Fprintf(&b, "\t\tsub%s := %s.Diff(%s)\n", f.Name, self, other)
		fmt.Fprintf(&b, "\t\tfor _, op := range sub%s.Operations {\n", f.Name)
		fmt.Fprintf(&b, "\t\t\tif op.Path == \"\" || op.Path == \"/\" { op.Path = \"/%s\" } else { op.Path = \"/%s\" + op.Path }\n", f.JSONName, f.JSONName)
		b.WriteString("\t\t\tp.Operations = append(p.Operations, op)\n\t\t}\n")
		if needsGuard {
			b.WriteString("\t}\n")
		}
	} else if f.IsCollection && !f.Atomic {
		if strings.HasPrefix(f.Type, "map[") {
			vt := mapVal(f.Type)
			ptrVal := isPtr(vt)
			fmt.Fprintf(&b, "\tif other.%s != nil {\n", f.Name)
			fmt.Fprintf(&b, "\t\tfor k, v := range other.%s {\n", f.Name)
			fmt.Fprintf(&b, "\t\t\tif t.%s == nil {\n", f.Name)
			fmt.Fprintf(&b, "\t\t\t\tp.Operations = append(p.Operations, %sOperation{Kind: %sOpReplace, Path: fmt.Sprintf(\"/%s/%%v\", k), New: v})\n", p, p, f.JSONName)
			b.WriteString("\t\t\t\tcontinue\n\t\t\t}\n")
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
			fmt.Fprintf(&b, "\t\t\t\tp.Operations = append(p.Operations, %sOperation{Kind: kind, Path: fmt.Sprintf(\"/%s/%%v\", k), Old: oldV, New: v})\n", p, f.JSONName)
			b.WriteString("\t\t\t}\n\t\t}\n\t}\n")
			fmt.Fprintf(&b, "\tif t.%s != nil {\n", f.Name)
			fmt.Fprintf(&b, "\t\tfor k, v := range t.%s {\n", f.Name)
			fmt.Fprintf(&b, "\t\t\tif other.%s == nil || !contains(other.%s, k) {\n", f.Name, f.Name)
			fmt.Fprintf(&b, "\t\t\t\tp.Operations = append(p.Operations, %sOperation{Kind: %sOpRemove, Path: fmt.Sprintf(\"/%s/%%v\", k), Old: v})\n", p, p, f.JSONName)
			b.WriteString("\t\t\t}\n\t\t}\n\t}\n")
		} else {
			// Slice
			elemType := sliceElem(f.Type)
			keyField := typeKeys[elemType]
			if keyField != "" {
				// Keyed slice diff
				fmt.Fprintf(&b, "\totherByKey := make(map[any]int)\n")
				fmt.Fprintf(&b, "\tfor i, v := range other.%s { otherByKey[v.%s] = i }\n", f.Name, keyField)
				fmt.Fprintf(&b, "\tfor _, v := range t.%s {\n", f.Name)
				fmt.Fprintf(&b, "\t\tif _, ok := otherByKey[v.%s]; !ok {\n", keyField)
				fmt.Fprintf(&b, "\t\t\tp.Operations = append(p.Operations, %sOperation{Kind: %sOpRemove, Path: fmt.Sprintf(\"/%s/%%v\", v.%s), Old: v})\n", p, p, f.JSONName, keyField)
				b.WriteString("\t\t}\n\t}\n")
				fmt.Fprintf(&b, "\ttByKey := make(map[any]int)\n")
				fmt.Fprintf(&b, "\tfor i, v := range t.%s { tByKey[v.%s] = i }\n", f.Name, keyField)
				fmt.Fprintf(&b, "\tfor _, v := range other.%s {\n", f.Name)
				fmt.Fprintf(&b, "\t\tif _, ok := tByKey[v.%s]; !ok {\n", keyField)
				fmt.Fprintf(&b, "\t\t\tp.Operations = append(p.Operations, %sOperation{Kind: %sOpAdd, Path: fmt.Sprintf(\"/%s/%%v\", v.%s), New: v})\n", p, p, f.JSONName, keyField)
				b.WriteString("\t\t}\n\t}\n")
			} else {
				fmt.Fprintf(&b, "\tif len(t.%s) != len(other.%s) {\n", f.Name, f.Name)
				fmt.Fprintf(&b, "\t\tp.Operations = append(p.Operations, %sOperation{Kind: %sOpReplace, Path: \"/%s\", Old: t.%s, New: other.%s})\n", p, p, f.JSONName, f.Name, f.Name)
				b.WriteString("\t} else {\n")
				fmt.Fprintf(&b, "\t\tfor i := range t.%s {\n", f.Name)
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
	} else if f.Generic() {
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
	fmt.Fprintf(&b, "\t\tif c.Op == \"type\" { return condition.CheckType(t.%s, c.Value.(string)), nil }\n", n)
	fmt.Fprintf(&b, "\t\tif c.Op == \"matches\" { return regexp.MatchString(c.Value.(string), fmt.Sprintf(\"%%v\", t.%s)) }\n", n)

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
			fmt.Fprintf(&b, "\tif %s != nil && !%s.Equal(%s) { return false }\n", self, self, other)
		} else {
			fmt.Fprintf(&b, "\tif !%s.Equal(%s) { return false }\n", self, other)
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
				fmt.Fprintf(&b, "\t\tif t.%s[i] != nil && !t.%s[i].Equal(other.%s[i]) { return false }\n", f.Name, f.Name, f.Name)
			case f.ElemGenerated:
				fmt.Fprintf(&b, "\t\tif !t.%s[i].Equal(&other.%s[i]) { return false }\n", f.Name, f.Name)
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
				b.WriteString("\t\tif v != nil && !v.Equal(vOther) { return false }\n")
			case f.ElemGenerated:
				b.WriteString("\t\tif !v.Equal(&vOther) { return false }\n")
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
	case f.IsText:
		return fmt.Sprintf("\t\t%s: append(%s(nil), t.%s...),\n", f.Name, f.Type, f.Name)
	case f.IsCollection && strings.HasPrefix(f.Type, "[]"):
		if elemNeedsCopy(f, sliceElem(f.Type)) {
			return fmt.Sprintf("\t\t%s: make(%s, len(t.%s)),\n", f.Name, f.Type, f.Name)
		}
		return fmt.Sprintf("\t\t%s: append(%s(nil), t.%s...),\n", f.Name, f.Type, f.Name)
	case f.IsCollection:
		return "" // map — handled in post-init phase
	case f.Opaque() && isPtr(f.Type):
		return "" // pointer to another package's type — copied in the post-init phase
	default:
		return fmt.Sprintf("\t\t%s: t.%s,\n", f.Name, f.Name)
	}
}

// copyFieldPost returns post-init deep-copy code for one field.
func copyFieldPost(f FieldInfo, p string) string {
	var b strings.Builder
	if f.Ignore {
		return ""
	}
	if f.Type == "" {
		// The generator cannot name the type, so it cannot copy it structurally.
		fmt.Fprintf(&b, "\tres.%s = %sClone(t.%s)\n", f.Name, p, f.Name)
		return b.String()
	}
	if f.Opaque() && isPtr(f.Type) {
		// Another package's type: copy the pointee so the clone does not share
		// state with the original.
		fmt.Fprintf(&b, "\tif t.%s != nil { _v := *t.%s; res.%s = &_v }\n", f.Name, f.Name, f.Name)
		return b.String()
	}
	if f.IsStruct {
		self := "(&t." + f.Name + ")"
		if isPtr(f.Type) {
			self = "t." + f.Name
			fmt.Fprintf(&b, "\tif %s != nil { res.%s = %s.Clone() }\n", self, f.Name, self)
		} else {
			fmt.Fprintf(&b, "\tres.%s = *%s.Clone()\n", f.Name, self)
		}
	}
	if f.IsCollection {
		if strings.HasPrefix(f.Type, "[]") {
			et := sliceElem(f.Type)
			switch {
			case isPtr(et) && f.ElemGenerated:
				fmt.Fprintf(&b, "\tfor i, v := range t.%s { if v != nil { res.%s[i] = v.Clone() } }\n", f.Name, f.Name)
			case isPtr(et):
				fmt.Fprintf(&b, "\tfor i, v := range t.%s { if v != nil { _v := *v; res.%s[i] = &_v } }\n", f.Name, f.Name)
			case f.ElemGenerated:
				fmt.Fprintf(&b, "\tfor i := range t.%s { res.%s[i] = *t.%s[i].Clone() }\n", f.Name, f.Name, f.Name)
			case isReferenceType(et):
				// A nested slice or map would be shared, not copied.
				fmt.Fprintf(&b, "\tfor i, v := range t.%s { res.%s[i] = %sClone(v) }\n", f.Name, f.Name, p)
			}
		} else if strings.HasPrefix(f.Type, "map[") {
			vt := mapVal(f.Type)
			fmt.Fprintf(&b, "\tif t.%s != nil {\n\t\tres.%s = make(%s)\n", f.Name, f.Name, f.Type)
			fmt.Fprintf(&b, "\t\tfor k, v := range t.%s {\n", f.Name)
			switch {
			case isPtr(vt) && f.ElemGenerated:
				fmt.Fprintf(&b, "\t\t\tif v != nil { res.%s[k] = v.Clone() }\n", f.Name)
			case isPtr(vt):
				fmt.Fprintf(&b, "\t\t\tif v != nil { _v := *v; res.%s[k] = &_v }\n", f.Name)
			case f.ElemGenerated:
				fmt.Fprintf(&b, "\t\t\tres.%s[k] = *v.Clone()\n", f.Name)
			case isReferenceType(vt):
				// A nested slice or map would be shared, not copied.
				fmt.Fprintf(&b, "\t\t\tres.%s[k] = %sClone(v)\n", f.Name, p)
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
			if err := {{.P}}ApplyOpReflection(t, op, logger); err != nil {
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
		if err != nil || !ok { return true, nil }
	}
	if op.Unless != nil {
		ok, err := t.evaluateCondition(*op.Unless)
		if err != nil || ok { return true, nil }
	}
	if op.Kind == {{.P}}OpLog {
		logger.Info("deep log", "message", op.New, "path", op.Path)
		return true, nil
	}

	switch op.Path {
	case "/":
		if op.Strict && (op.Kind == {{.P}}OpReplace || op.Kind == {{.P}}OpRemove) {
			old, ok := op.Old.({{.TypeName}})
			if !ok || !{{.P}}Equal(*t, old) {
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
	`// Diff compares t with other and returns a Patch.
func (t *{{.TypeName}}) Diff(other *{{.TypeName}}) {{.P}}Patch[{{.TypeName}}] {
	p := {{.P}}Patch[{{.TypeName}}]{}
{{range .Fields}}{{diffFieldCode . $.P $.TypeKeys}}{{end}}
	return p
}

`))

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
	return false, fmt.Errorf("unsupported condition path or op: %s", c.Path)
}

`))

var equalTmpl = template.Must(template.New("equal").Funcs(tmplFuncs).Parse(
	`// Equal returns true if t and other are deeply equal.
func (t *{{.TypeName}}) Equal(other *{{.TypeName}}) bool {
{{range .Fields}}{{if not .Ignore}}{{equalFieldCode . $.P}}{{end}}{{end -}}
	return true
}

`))

var copyTmpl = template.Must(template.New("copy").Funcs(tmplFuncs).Parse(
	`// Clone returns a deep copy of t.
func (t *{{.TypeName}}) Clone() *{{.TypeName}} {
	res := &{{.TypeName}}{
{{range .Fields}}{{if not .Ignore}}{{copyFieldInit .}}{{end}}{{end -}}
	}
{{range .Fields}}{{if not .Ignore}}{{copyFieldPost . $.P}}{{end}}{{end -}}
	return res
}
`))

var helpersTmpl = template.Must(template.New("helpers").Funcs(tmplFuncs).Parse(
	`
func contains[M ~map[K]V, K comparable, V any](m M, k K) bool {
	_, ok := m[k]
	return ok
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
		NeedsRegexp:    usesQualifier(body, "regexp"),
		NeedsStrings:   usesQualifier(body, "strings"),
		NeedsCondition: usesQualifier(body, "condition"),
		NeedsDeep:      g.pkgName != "deep" && usesQualifier(body, "deep"),
		NeedsCrdt:      g.pkgName != "deep" && usesQualifier(body, "crdt"),
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
	d := typeData{TypeName: typeName, P: g.pkgPrefix, Fields: fields, TypeKeys: g.typeKeys}
	must(patchTmpl.Execute(&g.body, d))
	must(applyOpTmpl.Execute(&g.body, d))
	must(diffTmpl.Execute(&g.body, d))
	must(evalCondTmpl.Execute(&g.body, d))
	must(equalTmpl.Execute(&g.body, d))
	must(copyTmpl.Execute(&g.body, d))
}

func (g *Generator) writeHelpers() {
	if g.pkgName == "deep" {
		return
	}
	must(helpersTmpl.Execute(&g.body, nil))
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
				},
				imports: make(map[string]string),
			}
		}

		requested := make(map[string]bool)
		for _, t := range strings.Split(*typeNames, ",") {
			requested[strings.TrimSpace(t)] = true
		}

		// Pass 1: index the package's own types and collect deep:"key" field
		// names.
		for _, file := range pkg.Files {
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
		g.writeHelpers()

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
		if len(field.Names) == 0 {
			continue // embedded field
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
		for _, nameIdent := range field.Names {
			name := nameIdent.Name
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
				Comparable:     ti.comparable,
				ElemComparable: ti.elemComparable,
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
}

// typeInfo is what the generator managed to work out about a field's type.
type typeInfo struct {
	name           string
	isStruct       bool
	isCollection   bool
	isText         bool
	elemGenerated  bool
	comparable     bool
	elemComparable bool
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
		ti.comparable = idx.isComparable(typ)
	case *ast.StarExpr:
		ti.isStruct = isGeneratedStruct(unparen(typ.X), idx)
	case *ast.ArrayType:
		// Fixed-size arrays are values, not collections: patching into them is
		// left to the reflection fallback.
		if typ.Len == nil {
			ti.isCollection = true
			ti.elemGenerated = isGeneratedStructRef(typ.Elt, idx)
			ti.elemComparable = idx.isComparable(unparen(typ.Elt))
		}
	case *ast.MapType:
		ti.isCollection = true
		ti.elemGenerated = isGeneratedStructRef(typ.Value, idx)
		ti.elemComparable = idx.isComparable(unparen(typ.Value))
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
