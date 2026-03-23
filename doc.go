// Package deep provides high-performance, type-safe deep diff, copy, equality,
// and patch-apply operations for Go values.
//
// # Architecture
//
// Deep operates on [Patch] values — flat, serializable lists of [Operation]
// records describing changes between two values of the same type. The four core
// operations are:
//
//   - [Diff] computes the patch from a to b.
//   - [Apply] applies a patch to a target pointer.
//   - [Equal] reports whether two values are deeply equal.
//   - [Clone] returns a deep copy of a value.
//
// # Code Generation
//
// For production use, run deep-gen to generate reflection-free implementations
// of all four operations for your types:
//
//	//go:generate go run github.com/brunoga/deep/v5/cmd/deep-gen -type=MyType .
//
// Generated code is 4–14x faster than the reflection fallback and is used
// automatically — no API changes required. The reflection engine remains as a
// transparent fallback for types without generated code.
//
// # Patch Construction
//
// Patches can be computed via [Diff] or built manually with [Edit].
// Typed operation constructors ([Set], [Add], [Remove], [Move], [Copy]) return
// an [Op] value that can be passed to [Builder.With] for a fluent, type-safe chain:
//
//	patch := deep.Edit(&user).
//	    With(
//	        deep.Set(nameField, "Alice"),
//	        deep.Set(ageField, 30).If(deep.Gt(ageField, 0)),
//	    ).
//	    Guard(deep.Gt(ageField, 18)).
//	    Build()
//
// [Field] creates type-safe path selectors from struct field accessors.
// [At] and [MapKey] extend paths into slices and maps with full type safety.
//
// # Conditions
//
// Per-operation guards are attached to [Op] values via [Op.If] and [Op.Unless].
// A global patch guard is set via [Builder.Guard] or [Patch.WithGuard]. Conditions
// are serializable and survive JSON/Gob round-trips.
//
// # Causality and CRDTs
//
// The [crdt] sub-package provides [crdt.LWW], a generic Last-Write-Wins
// register; [crdt.CRDT], a concurrency-safe wrapper for any type; and
// [crdt.Text], a convergent collaborative text type.
//
// # Serialization
//
// [Patch] marshals to/from JSON and Gob natively. Call [Register] for each
// type T whose values flow through [Operation.Old] or [Operation.New] fields
// during Gob encoding. [Patch.ToJSONPatch] and [ParseJSONPatch] interoperate
// with RFC 6902 JSON Patch (with deep extensions for conditions and causality).
package deep
