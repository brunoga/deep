# v5 Implementation Plan: Prototype Phase

The goal of this phase is to make the NOP API functional using a reflection-based fallback, setting the stage for the code generator.

## Step 1: Runtime Path Extraction (selector_runtime.go)
Implement a mechanism to turn `func(*T) *V` into a string path.
- [ ] Create a `PathCache` to avoid re-calculating offsets.
- [ ] Use `reflect` to map struct field offsets to JSON pointer strings.
- [ ] Implement `Field()` to resolve paths at first use.

## Step 2: The Flat Engine (engine_core.go)
Implement the core logic for the new data-oriented `Patch[T]`.
- [ ] **Apply:** A simple loop over `Operations`.
- [ ] **Value Resolution:** Use `internal/core` to handle the actual value setting/reflection for the fallback.
- [ ] **Safety:** Ensure `Apply` validates that the `Patch[T]` matches the target `*T`.

## Step 3: Minimal Diff (diff_core.go)
Implement a basic `Diff` that produces the new flat `Operation` slice.
- [ ] Reuse v4's recursive logic but flatten the output into the new `Patch` structure.
- [ ] Focus on correct `OpKind` mapping.

## Step 4: Performance Baseline
- [ ] Benchmark v5 (Flat/Reflection) vs v4 (Tree/Reflection).
- [ ] Goal: v5 should be simpler to reason about, even if reflection speed is similar.

## Step 5: The Generator Bootstrap (cmd/deep-gen)
- [ ] Scaffold the CLI tool.
- [ ] Implement a basic template that generates a `Path()` method for a struct, returning the pre-computed string paths.
