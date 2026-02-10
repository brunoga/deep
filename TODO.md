# Roadmap & TODO

This document tracks planned features and improvements to make the `deep` library a robust engine for state management and data synchronization.

## 1. Control & Customization
- [ ] **Struct Tags Support**: Implement `deep` tags to control library behavior per field.
    - `deep:"-"`: Ignore the field for both Copy and Diff.
    - `deep:"readonly"`: Field is included in Diff but cannot be modified by a Patch.
    - `deep:"atomic"`: Treat a complex struct/map as a scalar value (replace entirely if changed, do not recurse).
- [ ] **Custom Differs**: Allow types to implement a `Differ` interface to provide their own optimized patch generation logic.

## 2. Advanced Data Synchronization
- [ ] **Keyed Slice Alignment**: Support "Identity" for slice elements to handle moves, insertions, and deletions gracefully.
    - Use `deep:"key:FieldName"` tags or registry to identify the primary key of a struct within a slice.
    - Update the diff algorithm to align by key before performing LCS/Myers diff.
- [ ] **Map Key Normalization**: Support custom equality logic for complex map keys.

## 3. Observability & Auditing
- [ ] **Patch Walking API**: Implement a visitor pattern to allow applications to inspect a patch without applying it.
    ```go
    patch.Walk(func(path Path, op OpKind, old, new any) error)
    ```
- [ ] **Human-Readable Reporting**: Provide a utility to generate audit-log style descriptions of patches (e.g., "Updated User.Email from X to Y").

## 4. Conflict Resolution
- [ ] **Three-Way Merge**: Implement logic to merge two patches derived from the same base state.
    - `Merge(base, patchA, patchB) (mergedPatch, conflicts)`
- [ ] **Interactive Application**: Allow `ApplyChecked` to return a list of specific field-level conflicts instead of a single error.

## 5. Performance & Optimization
- [ ] **Reflection Cache**: Cache structural metadata (field offsets, types) to reduce reflection overhead in hot loops.
- [ ] **Zero-Allocation Diffs**: Explore pooling and pre-allocation strategies for large structural diffs.
- [ ] **SIMD Comparisons**: Investigate using SIMD for basic type comparisons in large slices/arrays.
