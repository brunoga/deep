# Roadmap

## Core Architecture

### Dependency-Aware Patch Application
Currently, `structPatch` uses a two-pass heuristic (Non-Destructive first, then Destructive) to handle basic Move operations (Copy + Remove) where the source might be removed before the destination reads it. While this solves common cases, it is an approximation.

**Goal:** Implement a formal dependency graph for patch operations.

**Implementation Sketch:**
1.  **Introspection:** Add a method to `diffPatch` (e.g., `GetDependencies() (reads []string, writes []string)`) allowing operations to declare the paths they access or mutate.
    *   `OpCopy` reads from `from` and writes to `path`.
    *   `OpRemove` writes/destroys `path`.
2.  **Graph Construction:** Before application, build a Directed Acyclic Graph (DAG) of operations where an edge exists if `Op A` writes to a path that `Op B` reads.
3.  **Topological Sort:** Execute operations in topologically sorted order to ensure dependencies are met.
4.  **Cycle Handling:** Explicitly detect cycles (e.g., Swap `A <-> B`) and handle them by creating temporary variables, rather than producing undefined behavior or crashes.

This will enable robust support for complex refactorings, swaps, and cross-structure moves without relying on implicit ordering or heuristics.
