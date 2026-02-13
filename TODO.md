# Roadmap & TODO

This document tracks planned features and improvements to make the `deep` library a robust engine for state management and data synchronization.

## 1. Control & Customization
- [x] **Struct Tags Support**: Implement `deep` tags to control library behavior per field.
    - `deep:"-"`: Ignore the field for both Copy and Diff.
    - `deep:"readonly"`: Field is included in Diff but cannot be modified by a Patch.
    - `deep:"atomic"`: Treat a complex struct/map as a scalar value (replace entirely if changed, do not recurse).
- [x] **Custom Differs**: Allow types to implement a `Differ` interface to provide their own optimized patch generation logic.
- [x] **Custom Type Registry**: Register custom diffing logic for third-party types (e.g., `time.Time`).

## 2. Advanced Data Synchronization
- [x] **Keyed Slice Alignment**: Support "Identity" for slice elements to handle moves, insertions, and deletions gracefully.
- [x] **Map Key Normalization**: Support custom equality logic for complex map keys via the `Keyer` interface.
- [x] **Move & Copy Detection**: Automatically detect relocated values during `Diff` and emit efficient operations.

## 3. Observability & Auditing
- [x] **Patch Walking API**: Implement a visitor pattern to allow applications to inspect a patch without applying it.
- [x] **Human-Readable Reporting**: Provide a utility (`Patch.Summary()`) to generate audit-log style descriptions.
- [x] **Multi-Error Reporting**: `ApplyChecked` returns all conflicts via `ApplyError` instead of stopping at the first failure.

## 4. Conflict Resolution
- [x] **Three-Way Merge**: Implement logic to merge two patches derived from the same base state.
    - `Merge(patchA, patchB) (mergedPatch, conflicts, error)`

## 5. Performance & Optimization
- [x] **Reflection Cache**: Cache structural metadata (field offsets, types) to reduce reflection overhead.
- [x] **Zero-Allocation Diffs**: Use pooling and lazy allocation to minimize GC pressure.
- [ ] **SIMD Comparisons**: Investigate using SIMD for basic type comparisons in large slices/arrays.

## 6. JSON Patch & RFC Interoperability
- [x] **Move & Copy Operations**: Implement internal `movePatch` and `copyPatch` to handle value re-ordering efficiently.
- [x] **Atomic Test Operation**: Allow patches to include "pre-condition only" paths (`OpTest`).
- [x] **JSON Pointer Support**: Standardize on RFC 6901 (`/path/to/item`) for all internal and external paths.
- [x] **Standard Export**: Provide a `ToJSONPatch()` method to generate RFC 6902 compliant JSON.
- [x] **Soft Conditions**: Support skipping operations (If/Unless logic) instead of failing on condition mismatch.
