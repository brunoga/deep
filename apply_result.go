package deep

import (
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"

	"github.com/brunoga/deep/v6/condition"
	"github.com/brunoga/deep/v6/internal/engine"
)

// ErrGuardNotMet is returned when a patch's [Patch.Guard] evaluates false. It
// is a sentinel so that a caller can tell a rejected patch — the state moved,
// try again — apart from a patch that could not be applied at all.
var ErrGuardNotMet = errors.New("patch guard not met")

// ErrPathNotAllowed is returned when an operation addresses a path outside the
// set given to [WithAllowedPaths]. See that option for why the whole patch is
// rejected rather than the offending operation dropped.
var ErrPathNotAllowed = errors.New("path not allowed")

// OpStatus is what became of one operation.
type OpStatus int

const (
	// StatusApplied means the operation ran and changed the target.
	StatusApplied OpStatus = iota
	// StatusSkipped means the operation's If or Unless condition did not hold,
	// so it was deliberately not applied. This is not an error: it is the
	// answer to the question the condition asked.
	StatusSkipped
	// StatusFailed means the operation could not be applied.
	StatusFailed
)

func (s OpStatus) String() string {
	switch s {
	case StatusApplied:
		return "applied"
	case StatusSkipped:
		return "skipped"
	case StatusFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// OpOutcome records what became of a single operation.
type OpOutcome struct {
	// Index is the operation's position in the patch it came from.
	Index int
	Path  string
	Kind  OpKind
	// Status is what became of the operation.
	Status OpStatus
	// Err is set when Status is StatusFailed.
	Err error
}

// ApplyResult reports what [ApplyWithResult] did, operation by operation.
//
// It exists because a skipped operation and an applied one are otherwise
// indistinguishable: [Apply] returns nil for both, so a caller applying a
// conditional patch cannot tell whether the condition held. For a patch used
// as a conditional write — the condition standing in for a compare-and-swap —
// that difference is the entire result of the call.
type ApplyResult struct {
	// Outcomes has one entry per operation, in patch order. An operation the
	// patch never reached, because the guard rejected it, has no entry.
	Outcomes []OpOutcome
}

// Counts returns how many operations landed in each status.
func (r *ApplyResult) Counts() (applied, skipped, failed int) {
	if r == nil {
		return 0, 0, 0
	}
	for _, o := range r.Outcomes {
		switch o.Status {
		case StatusApplied:
			applied++
		case StatusSkipped:
			skipped++
		case StatusFailed:
			failed++
		}
	}
	return applied, skipped, failed
}

// AllApplied reports whether every operation ran. It is false when any
// operation was skipped by its condition or failed, which for a conditional
// write is the signal to re-read and retry.
func (r *ApplyResult) AllApplied() bool {
	if r == nil {
		return false
	}
	for _, o := range r.Outcomes {
		if o.Status != StatusApplied {
			return false
		}
	}
	return true
}

// WithStatus returns the outcomes in the given status, in patch order.
func (r *ApplyResult) WithStatus(s OpStatus) []OpOutcome {
	if r == nil {
		return nil
	}
	var out []OpOutcome
	for _, o := range r.Outcomes {
		if o.Status == s {
			out = append(out, o)
		}
	}
	return out
}

// String summarises the result, listing the paths that did not apply.
func (r *ApplyResult) String() string {
	applied, skipped, failed := r.Counts()
	var b strings.Builder
	fmt.Fprintf(&b, "%d applied, %d skipped, %d failed", applied, skipped, failed)
	for _, o := range r.Outcomes {
		if o.Status == StatusSkipped {
			fmt.Fprintf(&b, "\n  skipped %s (condition not met)", o.Path)
		}
		if o.Status == StatusFailed {
			fmt.Fprintf(&b, "\n  failed %s: %v", o.Path, o.Err)
		}
	}
	return b.String()
}

// ApplyWithResult applies a patch and reports what became of every operation.
//
// It behaves as [Apply] does — operations run in order, each condition sees the
// state the operations before it left, and a failure does not stop the ones
// after it — but it returns an [ApplyResult] saying which operations applied,
// which their conditions skipped, and which failed.
//
// The returned error covers the patch as a whole: [ErrGuardNotMet] when the
// guard rejected it, [ErrPathNotAllowed] when an operation addressed a path
// outside [WithAllowedPaths], or an *[ApplyError] collecting the individual
// failures. A patch whose operations were all skipped returns a nil error —
// nothing failed — which is why the result, not the error, is what a
// conditional write should be judged on.
func ApplyWithResult[T any](target *T, p Patch[T], opts ...ApplyOption) (*ApplyResult, error) {
	v := reflect.ValueOf(target)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return nil, fmt.Errorf("target must be a non-nil pointer")
	}
	cfg := newApplyConfig(opts...)
	res := &ApplyResult{}

	if err := cfg.checkPaths(p.Operations); err != nil {
		return res, err
	}

	if p.Guard != nil {
		ok, err := condition.Evaluate(v.Elem(), p.Guard)
		if err != nil {
			return res, fmt.Errorf("global condition evaluation failed: %w", err)
		}
		if !ok {
			return res, ErrGuardNotMet
		}
	}

	// One operation at a time, so that a condition can be evaluated here —
	// where a skip can be recorded — rather than inside the apply path, which
	// has no way to report one. The scratch slice is reused across operations;
	// the apply path only reads it.
	scratch := make([]Operation, 1)

	var errs []error
	for i, op := range p.Operations {
		outcome := OpOutcome{Index: i, Path: op.Path, Kind: op.Kind}

		skipped, err := conditionsHold(v.Elem(), op)
		switch {
		case err != nil:
			outcome.Status, outcome.Err = StatusFailed, err
			errs = append(errs, err)
			res.Outcomes = append(res.Outcomes, outcome)
			continue
		case skipped:
			outcome.Status = StatusSkipped
			res.Outcomes = append(res.Outcomes, outcome)
			continue
		}

		// The conditions have been answered already; clear them so the apply
		// path does not evaluate them a second time.
		bare := op
		bare.If, bare.Unless = nil, nil

		if err := applyOneOp(target, bare, p.Strict, cfg.logger, scratch); err != nil {
			outcome.Status, outcome.Err = StatusFailed, err
			errs = append(errs, err)
		} else {
			outcome.Status = StatusApplied
		}
		res.Outcomes = append(res.Outcomes, outcome)
	}

	if len(errs) > 0 {
		return res, &ApplyError{Errors: errs}
	}
	return res, nil
}

// conditionsHold evaluates an operation's If and Unless against v. It reports
// whether the operation should be skipped, and any error evaluating them.
func conditionsHold(v reflect.Value, op Operation) (skip bool, err error) {
	if op.If != nil {
		ok, err := condition.Evaluate(v, op.If)
		if err != nil {
			return false, fmt.Errorf("condition evaluation failed at %s: %w", op.Path, err)
		}
		if !ok {
			return true, nil
		}
	}
	if op.Unless != nil {
		ok, err := condition.Evaluate(v, op.Unless)
		if err != nil {
			return false, fmt.Errorf("condition evaluation failed at %s: %w", op.Path, err)
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

// applyOneOp applies a single operation, through the generated fast path when
// the type has one and through the reflection engine otherwise.
func applyOneOp[T any](target *T, op Operation, strict bool, logger *slog.Logger, scratch []Operation) error {
	if patcher, ok := any(target).(interface {
		Patch(Patch[T], *slog.Logger) error
	}); ok {
		scratch[0] = op
		err := patcher.Patch(Patch[T]{Operations: scratch, Strict: strict}, logger)
		// A single operation produces at most one failure; report it directly
		// rather than wrapped in a collection of one.
		var ae *ApplyError
		if errors.As(err, &ae) && len(ae.Errors) == 1 {
			return ae.Errors[0]
		}
		return err
	}
	op.Strict = strict
	return engine.ApplyOpReflectionValue(reflect.ValueOf(target).Elem(), op, logger)
}
