package deep

import (
	"fmt"
	"log/slog"
	"reflect"
	"sort"
	"strings"

	"github.com/brunoga/deep/v5/condition"
	icore "github.com/brunoga/deep/v5/internal/core"
	"github.com/brunoga/deep/v5/internal/engine"
)

type applyConfig struct {
	logger       *slog.Logger
	allowedPaths []string
}

func newApplyConfig(opts ...ApplyOption) applyConfig {
	cfg := applyConfig{logger: slog.Default()}
	for _, o := range opts {
		o(&cfg)
	}
	return cfg
}

// checkPaths rejects the whole patch if any operation addresses a path outside
// the allowed set. Nothing is applied in that case: a patch that reached for a
// path it was not entitled to is not one to apply the rest of.
func (c applyConfig) checkPaths(ops []Operation) error {
	if c.allowedPaths == nil {
		return nil
	}
	for _, op := range ops {
		for _, p := range []string{op.Path, op.From} {
			if p == "" {
				continue
			}
			if !pathAllowed(p, c.allowedPaths) {
				return fmt.Errorf("%w: %s", ErrPathNotAllowed, p)
			}
		}
	}
	return nil
}

func pathAllowed(path string, allowed []string) bool {
	for _, a := range allowed {
		if a == "/" || path == a || strings.HasPrefix(path, strings.TrimSuffix(a, "/")+"/") {
			return true
		}
	}
	return false
}

// ApplyOption configures the behaviour of [Apply].
type ApplyOption func(*applyConfig)

// WithLogger sets the [slog.Logger] used for [OpLog] operations within a
// single [Apply] call. If not provided, [slog.Default] is used.
func WithLogger(l *slog.Logger) ApplyOption {
	return func(c *applyConfig) { c.logger = l }
}

// WithAllowedPaths restricts a patch to the given path prefixes. An operation
// addressing anything else — including through the From path of a move or a
// copy, which reads from wherever it points — makes the whole call fail with
// [ErrPathNotAllowed], leaving the target untouched.
//
// A patch that arrived from somewhere else is a program someone else wrote,
// running against your data. [Struct tags] already keep fields out of reach:
// `deep:"-"` hides a field entirely and `deep:"readonly"` makes writing it an
// error. This is the complement for cases where the type is shared and the
// restriction belongs to one caller rather than to the type — a service that
// should only ever touch /status, say.
//
// A prefix matches itself and everything under it: "/a" allows "/a" and
// "/a/b", but not "/ab". Passing "/" allows everything, which is the same as
// not passing the option at all.
//
// [Struct tags]: https://github.com/brunoga/deep#struct-tags
func WithAllowedPaths(prefixes ...string) ApplyOption {
	return func(c *applyConfig) { c.allowedPaths = append(c.allowedPaths, prefixes...) }
}

// Apply applies a Patch to a target pointer.
// v5 prioritizes the generated Patch method but falls back to reflection if needed.
//
// Note: when a Patch has been serialized to JSON and decoded, numeric values in
// Operation.Old and Operation.New will be float64 regardless of the original type.
// This affects strict-mode Old-value checks.
func Apply[T any](target *T, p Patch[T], opts ...ApplyOption) error {
	v := reflect.ValueOf(target)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return fmt.Errorf("target must be a non-nil pointer")
	}

	cfg := newApplyConfig(opts...)

	if err := cfg.checkPaths(p.Operations); err != nil {
		return err
	}

	// Dispatch to generated Patch method if available.
	if patcher, ok := any(target).(interface {
		Patch(Patch[T], *slog.Logger) error
	}); ok {
		return patcher.Patch(p, cfg.logger)
	}

	// Reflection fallback.

	if p.Guard != nil {
		ok, err := condition.Evaluate(v.Elem(), p.Guard)
		if err != nil {
			return fmt.Errorf("global condition evaluation failed: %w", err)
		}
		if !ok {
			return ErrGuardNotMet
		}
	}

	var errors []error
	for _, op := range p.Operations {
		op.Strict = p.Strict
		if err := engine.ApplyOpReflectionValue(v.Elem(), op, cfg.logger); err != nil {
			errors = append(errors, err)
		}
	}

	if len(errors) > 0 {
		return &ApplyError{Errors: errors}
	}
	return nil
}

// ConflictResolver defines how to resolve merge conflicts.
type ConflictResolver interface {
	Resolve(path string, local, remote any) any
}

// Merge combines two patches into a single patch, resolving conflicts.
//
// Operations are matched by path. When both patches write the same path,
// r.Resolve decides the value if r is non-nil; otherwise other's operation
// wins.
//
// Paths that are not equal can still collide: an operation on /user and one on
// /user/name are not independent, and keeping both produced a patch that could
// not be applied — removing /user and then writing /user/name fails on the
// path that is no longer there. When the two sides disagree that way, the
// operation from other wins and the one it encloses (or is enclosed by) is
// dropped. r is not consulted for these, because there is no single path at
// which to ask.
//
// An ancestor and a descendant from the *same* patch are left alone: writing
// /user and then /user/name within one patch is a legitimate sequence, and the
// path ordering below applies them in that order.
//
// The output is sorted by path, which makes the result deterministic and puts
// an ancestor before its descendants.
func Merge[T any](base, other Patch[T], r ConflictResolver) Patch[T] {
	type entry struct {
		op      Operation
		isOther bool
	}
	latest := make(map[string]entry, len(base.Operations)+len(other.Operations))

	mergeOps := func(ops []Operation, isOther bool) {
		for _, op := range ops {
			existing, ok := latest[op.Path]
			if !ok {
				latest[op.Path] = entry{op, isOther}
				continue
			}
			if r != nil {
				op.New = r.Resolve(op.Path, existing.op.New, op.New)
				latest[op.Path] = entry{op, isOther}
			} else if isOther {
				latest[op.Path] = entry{op, isOther}
			}
		}
	}

	mergeOps(base.Operations, false)
	mergeOps(other.Operations, true)

	// Drop whichever side of an ancestor/descendant collision did not come
	// from other. Both directions matter: other may hold the ancestor or the
	// descendant.
	for path, e := range latest {
		for otherPath, oe := range latest {
			if path == otherPath || e.isOther == oe.isOther {
				continue
			}
			if pathEncloses(path, otherPath) && !e.isOther {
				// This operation is enclosed by one from the other side, or
				// encloses it; the one from other survives.
				delete(latest, path)
				break
			}
			if pathEncloses(otherPath, path) && !e.isOther {
				delete(latest, path)
				break
			}
		}
	}

	res := Patch[T]{}
	res.Operations = make([]Operation, 0, len(latest))
	for _, e := range latest {
		res.Operations = append(res.Operations, e.op)
	}
	sort.Slice(res.Operations, func(i, j int) bool {
		return res.Operations[i].Path < res.Operations[j].Path
	})
	return res
}

// pathEncloses reports whether ancestor contains descendant: the same path, or
// a proper prefix of it at a segment boundary. "/a" encloses "/a/b" but not
// "/ab".
func pathEncloses(ancestor, descendant string) bool {
	if ancestor == descendant {
		return true
	}
	if ancestor == "" || ancestor == "/" {
		return true
	}
	return strings.HasPrefix(descendant, strings.TrimSuffix(ancestor, "/")+"/")
}

// Equal returns true if a and b are deeply equal.
func Equal[T any](a, b T) bool {
	if equallable, ok := any(&a).(interface {
		Equal(*T) bool
	}); ok {
		return equallable.Equal(&b)
	}

	return engine.Equal(a, b)
}

// EqualCoerced reports whether current equals expected, where expected may be
// of a different but losslessly convertible type.
//
// [Operation.Old] and a condition's value are declared `any`, so a patch that
// travelled as JSON carries whatever the decoder produced — every number
// arrives as float64, a nested struct as map[string]any — regardless of the
// types the patch was built from. [Equal] requires identical types and reports
// those as mismatches, which is why a strict check against a decoded patch can
// fail on state it actually matches.
//
// The conversion is verified rather than trusted: converting back has to
// reproduce the value given, so float64(5.7) does not match an int field
// holding 5.
//
// This is what generated code and the reflection engine use for strict-mode
// checks. It is exported so a server applying patches from the wire can make
// the same comparison itself.
func EqualCoerced(current, expected any) bool {
	return icore.EqualCoerced(reflect.ValueOf(current), expected)
}

// Clone returns a deep copy of v.
func Clone[T any](v T) T {
	if copyable, ok := any(&v).(interface {
		Clone() *T
	}); ok {
		return *copyable.Clone()
	}

	res, _ := engine.Copy(v)
	return res
}
