package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"log"
	"os"
	"reflect"
	"strings"
	"text/template"
)

var (
	typeNames  = flag.String("type", "", "comma-separated list of type names; must be set")
	outputFile = flag.String("output", "", "output file name; defaults to stdout")
)

// FieldInfo describes one struct field for code generation.
type FieldInfo struct {
	Name         string
	JSONName     string
	Type         string
	IsStruct     bool
	IsCollection bool
	IsText       bool
	Ignore       bool
	ReadOnly     bool
	Atomic       bool
}

// Generator accumulates generated source for all requested types.
type Generator struct {
	pkgName   string
	pkgPrefix string // "deep." for non-deep packages, "" when generating inside the deep package
	buf       bytes.Buffer
	typeKeys  map[string]string // typeName -> keyFieldName (from deep:"key" tag)
}

// ── template data structs ────────────────────────────────────────────────────

type headerData struct {
	PkgName      string
	NeedsRegexp  bool
	NeedsReflect bool
	NeedsStrings bool
	NeedsDeep    bool
	NeedsCrdt    bool
}

type typeData struct {
	TypeName string
	P        string // package prefix
	Fields   []FieldInfo
	TypeKeys map[string]string
}

// ── helpers used by both templates and FuncMap ───────────────────────────────

func isPtr(s string) bool          { return strings.HasPrefix(s, "*") }
func deref(s string) string        { return strings.TrimPrefix(s, "*") }
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
	fmt.Fprintf(&b, "\t\t\t%sLogger().Info(\"deep log\", \"message\", op.New, \"path\", op.Path, \"field\", t.%s)\n", p, f.Name)
	b.WriteString("\t\t\treturn true, nil\n\t\t}\n")
	// Strict check
	fmt.Fprintf(&b, "\t\tif op.Kind == %sOpReplace && op.Strict {\n", p)
	if f.IsStruct || f.IsText || f.IsCollection {
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
				fmt.Fprintf(&b, "\t\t\t\treturn %s.ApplyOperation(op)\n\t\t\t}\n", selfArg)
			} else {
				fmt.Fprintf(&b, "\t\t\top.Path = op.Path[len(\"/%s/\")-1:]\n", f.JSONName)
				fmt.Fprintf(&b, "\t\t\treturn %s.ApplyOperation(op)\n", selfArg)
			}
		}
		b.WriteString("\t\t}\n")
	}
	if f.IsCollection && isMapStringKey(f.Type) {
		vt := mapVal(f.Type)
		fmt.Fprintf(&b, "\t\tif strings.HasPrefix(op.Path, \"/%s/\") {\n", f.JSONName)
		if f.ReadOnly {
			b.WriteString("\t\t\treturn true, fmt.Errorf(\"field %s is read-only\", op.Path)\n")
		} else if isPtr(vt) {
			fmt.Fprintf(&b, "\t\t\tparts := strings.Split(op.Path[len(\"/%s/\"):], \"/\")\n", f.JSONName)
			b.WriteString("\t\t\tkey := parts[0]\n")
			fmt.Fprintf(&b, "\t\t\tif val, ok := t.%s[key]; ok && val != nil {\n", f.Name)
			b.WriteString("\t\t\t\top.Path = \"/\"\n")
			b.WriteString("\t\t\t\tif len(parts) > 1 { op.Path = \"/\" + strings.Join(parts[1:], \"/\") }\n")
			b.WriteString("\t\t\t\treturn val.ApplyOperation(op)\n\t\t\t}\n")
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
		needsGuard := isPtr(f.Type) || f.IsText
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
			if ptrVal {
				b.WriteString("!oldV.Equal(v) {\n")
			} else {
				b.WriteString("v != oldV {\n")
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
				fmt.Fprintf(&b, "\t\t\tif t.%s[i] != other.%s[i] {\n", f.Name, f.Name)
				fmt.Fprintf(&b, "\t\t\t\tp.Operations = append(p.Operations, %sOperation{Kind: %sOpReplace, Path: fmt.Sprintf(\"/%s/%%d\", i), Old: t.%s[i], New: other.%s[i]})\n", p, p, f.JSONName, f.Name, f.Name)
				b.WriteString("\t\t\t}\n\t\t}\n\t}\n")
			}
		}
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
	fmt.Fprintf(&b, "\t\tif c.Op == \"type\" { return checkType(t.%s, c.Value.(string)), nil }\n", n)
	fmt.Fprintf(&b, "\t\tif c.Op == \"log\" { %sLogger().Info(\"deep condition log\", \"message\", c.Value, \"path\", c.Path, \"value\", t.%s); return true, nil }\n", pkgPrefix, n)
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
func equalFieldCode(f FieldInfo) string {
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
			if ptrElem {
				fmt.Fprintf(&b, "\t\tif (t.%s[i] == nil) != (other.%s[i] == nil) { return false }\n", f.Name, f.Name)
				fmt.Fprintf(&b, "\t\tif t.%s[i] != nil && !t.%s[i].Equal(other.%s[i]) { return false }\n", f.Name, f.Name, f.Name)
			} else if f.IsStruct {
				fmt.Fprintf(&b, "\t\tif !t.%s[i].Equal(&other.%s[i]) { return false }\n", f.Name, f.Name)
			} else {
				fmt.Fprintf(&b, "\t\tif t.%s[i] != other.%s[i] { return false }\n", f.Name, f.Name)
			}
			b.WriteString("\t}\n")
		} else if strings.HasPrefix(f.Type, "map[") {
			vt := mapVal(f.Type)
			ptrVal := isPtr(vt)
			fmt.Fprintf(&b, "\tfor k, v := range t.%s {\n", f.Name)
			fmt.Fprintf(&b, "\t\tvOther, ok := other.%s[k]\n", f.Name)
			b.WriteString("\t\tif !ok { return false }\n")
			if ptrVal {
				b.WriteString("\t\tif (v == nil) != (vOther == nil) { return false }\n")
				b.WriteString("\t\tif v != nil && !v.Equal(vOther) { return false }\n")
			} else if f.IsStruct {
				b.WriteString("\t\tif !v.Equal(&vOther) { return false }\n")
			} else {
				b.WriteString("\t\tif v != vOther { return false }\n")
			}
			b.WriteString("\t}\n")
		}
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
	case f.IsText:
		return fmt.Sprintf("\t\t%s: append(%s(nil), t.%s...),\n", f.Name, f.Type, f.Name)
	case f.IsCollection && strings.HasPrefix(f.Type, "[]"):
		et := sliceElem(f.Type)
		if isPtr(et) || f.IsStruct {
			return fmt.Sprintf("\t\t%s: make(%s, len(t.%s)),\n", f.Name, f.Type, f.Name)
		}
		return fmt.Sprintf("\t\t%s: append(%s(nil), t.%s...),\n", f.Name, f.Type, f.Name)
	case f.IsCollection:
		return "" // map — handled in post-init phase
	default:
		return fmt.Sprintf("\t\t%s: t.%s,\n", f.Name, f.Name)
	}
}

// copyFieldPost returns post-init deep-copy code for one field.
func copyFieldPost(f FieldInfo) string {
	var b strings.Builder
	if f.Ignore {
		return ""
	}
	if f.IsStruct {
		self := "(&t." + f.Name + ")"
		if isPtr(f.Type) {
			self = "t." + f.Name
			fmt.Fprintf(&b, "\tif %s != nil { res.%s = %s.Copy() }\n", self, f.Name, self)
		} else {
			fmt.Fprintf(&b, "\tres.%s = *%s.Copy()\n", f.Name, self)
		}
	}
	if f.IsCollection {
		if strings.HasPrefix(f.Type, "[]") {
			et := sliceElem(f.Type)
			if isPtr(et) {
				fmt.Fprintf(&b, "\tfor i, v := range t.%s { if v != nil { res.%s[i] = v.Copy() } }\n", f.Name, f.Name)
			} else if f.IsStruct {
				fmt.Fprintf(&b, "\tfor i := range t.%s { res.%s[i] = *t.%s[i].Copy() }\n", f.Name, f.Name, f.Name)
			}
		} else if strings.HasPrefix(f.Type, "map[") {
			vt := mapVal(f.Type)
			fmt.Fprintf(&b, "\tif t.%s != nil {\n\t\tres.%s = make(%s)\n", f.Name, f.Name, f.Type)
			fmt.Fprintf(&b, "\t\tfor k, v := range t.%s {\n", f.Name)
			if isPtr(vt) {
				fmt.Fprintf(&b, "\t\t\tif v != nil { res.%s[k] = v.Copy() }\n", f.Name)
			} else if f.IsStruct {
				fmt.Fprintf(&b, "\t\t\tres.%s[k] = *v.Copy()\n", f.Name)
			} else {
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
{{- if .NeedsRegexp}}
	"regexp"
{{- end}}
{{- if .NeedsReflect}}
	"reflect"
{{- end}}
{{- if .NeedsStrings}}
	"strings"
{{- end}}
{{- if .NeedsDeep}}
	deep "github.com/brunoga/deep/v5"
{{- end}}
{{- if .NeedsCrdt}}
	crdt "github.com/brunoga/deep/v5/crdt"
{{- end}}
)
`))

var applyOpTmpl = template.Must(template.New("applyOp").Funcs(tmplFuncs).Parse(
	`// ApplyOperation applies a single operation to {{.TypeName}} efficiently.
func (t *{{.TypeName}}) ApplyOperation(op {{.P}}Operation) (bool, error) {
	if op.If != nil {
		ok, err := t.EvaluateCondition(*op.If)
		if err != nil || !ok { return true, err }
	}
	if op.Unless != nil {
		ok, err := t.EvaluateCondition(*op.Unless)
		if err == nil && ok { return true, nil }
	}

	if op.Path == "" || op.Path == "/" {
		if v, ok := op.New.({{.TypeName}}); ok {
			*t = v
			return true, nil
		}
		if m, ok := op.New.(map[string]any); ok {
			for k, v := range m {
				t.ApplyOperation({{.P}}Operation{Kind: op.Kind, Path: "/" + k, New: v})
			}
			return true, nil
		}
	}

	switch op.Path {
{{range .Fields}}{{if not .Ignore}}{{fieldApplyCase . $.P}}{{end}}{{end -}}
	default:
{{range .Fields}}{{delegateCase . $.P}}{{end -}}
	}
	return false, nil
}

`))

var diffTmpl = template.Must(template.New("diff").Funcs(tmplFuncs).Parse(
	`// Diff compares t with other and returns a Patch.
func (t *{{.TypeName}}) Diff(other *{{.TypeName}}) {{.P}}Patch[{{.TypeName}}] {
	p := {{.P}}NewPatch[{{.TypeName}}]()
{{range .Fields}}{{diffFieldCode . $.P $.TypeKeys}}{{end}}
	return p
}

`))

var evalCondTmpl = template.Must(template.New("evalCond").Funcs(tmplFuncs).Parse(
	`func (t *{{.TypeName}}) EvaluateCondition(c {{.P}}Condition) (bool, error) {
	switch c.Op {
	case "and":
		for _, sub := range c.Sub {
			ok, err := t.EvaluateCondition(*sub)
			if err != nil || !ok { return false, err }
		}
		return true, nil
	case "or":
		for _, sub := range c.Sub {
			ok, err := t.EvaluateCondition(*sub)
			if err == nil && ok { return true, nil }
		}
		return false, nil
	case "not":
		if len(c.Sub) > 0 {
			ok, err := t.EvaluateCondition(*c.Sub[0])
			if err != nil { return false, err }
			return !ok, nil
		}
		return true, nil
	}

	switch c.Path {
{{range .Fields}}{{if and (not .Ignore) (not .IsStruct) (not .IsCollection) (not .IsText) -}}
	{{if ne .JSONName .Name}}case "/{{.JSONName}}", "/{{.Name}}":{{else}}case "/{{.Name}}":{{end}}
{{evalCondCase . $.P}}{{end}}{{end -}}
	}
	return false, fmt.Errorf("unsupported condition path or op: %s", c.Path)
}

`))

var equalTmpl = template.Must(template.New("equal").Funcs(tmplFuncs).Parse(
	`// Equal returns true if t and other are deeply equal.
func (t *{{.TypeName}}) Equal(other *{{.TypeName}}) bool {
{{range .Fields}}{{if not .Ignore}}{{equalFieldCode .}}{{end}}{{end -}}
	return true
}

`))

var copyTmpl = template.Must(template.New("copy").Funcs(tmplFuncs).Parse(
	`// Copy returns a deep copy of t.
func (t *{{.TypeName}}) Copy() *{{.TypeName}} {
	res := &{{.TypeName}}{
{{range .Fields}}{{if not .Ignore}}{{copyFieldInit .}}{{end}}{{end -}}
	}
{{range .Fields}}{{if not .Ignore}}{{copyFieldPost .}}{{end}}{{end -}}
	return res
}
`))

var helpersTmpl = template.Must(template.New("helpers").Funcs(tmplFuncs).Parse(
	`
func contains[M ~map[K]V, K comparable, V any](m M, k K) bool {
	_, ok := m[k]
	return ok
}

func checkType(v any, typeName string) bool {
	switch typeName {
	case "string":
		_, ok := v.(string)
		return ok
	case "number":
		switch v.(type) {
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
			return true
		}
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "object":
		rv := reflect.ValueOf(v)
		return rv.Kind() == reflect.Struct || rv.Kind() == reflect.Map
	case "array":
		rv := reflect.ValueOf(v)
		return rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array
	case "null":
		if v == nil { return true }
		rv := reflect.ValueOf(v)
		return (rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface ||
			rv.Kind() == reflect.Slice || rv.Kind() == reflect.Map) && rv.IsNil()
	}
	return false
}
`))

// ── generator ────────────────────────────────────────────────────────────────

func (g *Generator) writeHeader(allFields []FieldInfo) {
	needsStrings, needsRegexp, needsCrdt := false, false, false
	for _, f := range allFields {
		if f.Ignore {
			continue
		}
		if (f.IsStruct && !f.Atomic) || (f.IsCollection && isMapStringKey(f.Type)) {
			needsStrings = true
		}
		if !f.IsStruct && !f.IsCollection && !f.IsText {
			needsRegexp = true
		}
		if f.IsText {
			needsCrdt = true
		}
	}
	must(headerTmpl.Execute(&g.buf, headerData{
		PkgName:      g.pkgName,
		NeedsRegexp:  needsRegexp,
		NeedsReflect: g.pkgName != "deep",
		NeedsStrings: needsStrings,
		NeedsDeep:    g.pkgName != "deep",
		NeedsCrdt:    needsCrdt && g.pkgName != "deep",
	}))
}

func (g *Generator) writeType(typeName string, fields []FieldInfo) {
	if g.pkgName != "deep" {
		g.pkgPrefix = "deep."
	}
	d := typeData{TypeName: typeName, P: g.pkgPrefix, Fields: fields, TypeKeys: g.typeKeys}
	must(applyOpTmpl.Execute(&g.buf, d))
	must(diffTmpl.Execute(&g.buf, d))
	must(evalCondTmpl.Execute(&g.buf, d))
	must(equalTmpl.Execute(&g.buf, d))
	must(copyTmpl.Execute(&g.buf, d))
}

func (g *Generator) writeHelpers() {
	if g.pkgName == "deep" {
		return
	}
	must(helpersTmpl.Execute(&g.buf, nil))
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

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, nil, 0)
	if err != nil {
		log.Fatal(err)
	}

	var g *Generator

	for pkgName, pkg := range pkgs {
		if strings.HasSuffix(pkgName, "_test") {
			continue
		}
		if g == nil {
			g = &Generator{pkgName: pkgName, typeKeys: make(map[string]string)}
		}

		requested := make(map[string]bool)
		for _, t := range strings.Split(*typeNames, ",") {
			requested[strings.TrimSpace(t)] = true
		}

		// Pass 1: collect deep:"key" field names.
		for _, file := range pkg.Files {
			ast.Inspect(file, func(n ast.Node) bool {
				ts, ok := n.(*ast.TypeSpec)
				if !ok {
					return true
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					return true
				}
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
			ast.Inspect(file, func(n ast.Node) bool {
				ts, ok := n.(*ast.TypeSpec)
				if !ok || !requested[ts.Name.Name] {
					return true
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					return true
				}
				fields := parseFields(st)
				allTypes = append(allTypes, ts.Name.Name)
				allFields = append(allFields, fields)
				return false
			})
		}

		if len(allTypes) == 0 {
			continue
		}

		var combined []FieldInfo
		for _, fs := range allFields {
			combined = append(combined, fs...)
		}
		g.writeHeader(combined)
		for i := range allTypes {
			g.writeType(allTypes[i], allFields[i])
		}
		g.writeHelpers()
	}

	if g == nil {
		return
	}

	src, err := format.Source(g.buf.Bytes())
	if err != nil {
		log.Printf("warning: gofmt failed: %v", err)
		src = g.buf.Bytes()
	}

	// Determine output file: explicit -output flag, or default to
	// "{first_type_lowercase}_deep.go" in the target directory (like stringer).
	outFile := *outputFile
	if outFile == "" {
		firstName := strings.ToLower(strings.SplitN(*typeNames, ",", 2)[0])
		outFile = dir + "/" + firstName + "_deep.go"
	}
	if err := os.WriteFile(outFile, src, 0644); err != nil {
		log.Fatalf("writing output: %v", err)
	}
	log.Printf("deep-gen: wrote %s", outFile)
}

func parseFields(st *ast.StructType) []FieldInfo {
	var fields []FieldInfo
	for _, field := range st.Fields.List {
		if len(field.Names) == 0 {
			continue
		}
		name := field.Names[0].Name
		jsonName := name
		var ignore, readOnly, atomic bool

		if field.Tag != nil {
			tagVal := strings.Trim(field.Tag.Value, "`")
			tag := reflect.StructTag(tagVal)
			if jt := tag.Get("json"); jt != "" {
				jsonName = strings.Split(jt, ",")[0]
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

		typeName, isStruct, isCollection, isText := resolveType(field.Type)
		fields = append(fields, FieldInfo{
			Name:         name,
			JSONName:     jsonName,
			Type:         typeName,
			IsStruct:     isStruct,
			IsCollection: isCollection,
			IsText:       isText,
			Ignore:       ignore,
			ReadOnly:     readOnly,
			Atomic:       atomic,
		})
	}
	return fields
}

func resolveType(expr ast.Expr) (typeName string, isStruct, isCollection, isText bool) {
	switch typ := expr.(type) {
	case *ast.Ident:
		typeName = typ.Name
		if typeName == "Text" {
			isText = true
			typeName = "crdt.Text"
		} else if len(typeName) > 0 && typeName[0] >= 'A' && typeName[0] <= 'Z' {
			switch typeName {
			case "String", "Int", "Bool", "Float64":
			default:
				isStruct = true
			}
		}
	case *ast.StarExpr:
		if ident, ok := typ.X.(*ast.Ident); ok {
			typeName = "*" + ident.Name
			isStruct = true
		}
	case *ast.SelectorExpr:
		if ident, ok := typ.X.(*ast.Ident); ok {
			if typ.Sel.Name == "Text" {
				isText = true
				typeName = "crdt.Text"
			} else if ident.Name == "deep" {
				typeName = "deep." + typ.Sel.Name
			} else {
				typeName = ident.Name + "." + typ.Sel.Name
			}
		}
	case *ast.ArrayType:
		isCollection = true
		switch elt := typ.Elt.(type) {
		case *ast.Ident:
			typeName = "[]" + elt.Name
		case *ast.StarExpr:
			if ident, ok := elt.X.(*ast.Ident); ok {
				typeName = "[]*" + ident.Name
			}
		default:
			typeName = "[]any"
		}
	case *ast.MapType:
		isCollection = true
		keyName, valName := "any", "any"
		if ident, ok := typ.Key.(*ast.Ident); ok {
			keyName = ident.Name
		}
		switch vtyp := typ.Value.(type) {
		case *ast.Ident:
			valName = vtyp.Name
		case *ast.StarExpr:
			if ident, ok := vtyp.X.(*ast.Ident); ok {
				valName = "*" + ident.Name
			}
		}
		typeName = fmt.Sprintf("map[%s]%s", keyName, valName)
	}
	return
}
