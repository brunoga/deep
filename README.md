# deep
Support for doing deep copies of (almost all) Go types.

This is a from scratch implementation of the ideas from https://github.com/barkimedes/go-deepcopy (which, unfortunatelly appears to be dead) but it is faster, simpler to use for callers and supports copying unexported fields.

It should support most Go types. Specificaly, it does not support functions, channels and unsafe.Pointers unless they are nil. Also it might have weird interactions with structs that include any synchronization primitives (mutexes, for example. They should still be copied but if they are usable after that is left as an exercise to the reader).

| Benchmark                          | Iterations | Time (ns/op) | Memory (B/op) | Allocations (allocs/op) |
|------------------------------------|------------|--------------|---------------|-------------------------|
| **BenchmarkCopy_Deep-10**          | **802910** | **1485**     | **1584**      | **28**                  |
| BenchmarkCopy_DeepCopy-10 (1)      | 527125     | 2259         | 1912          | 50                      |
| BenchmarkCopy_CopyStructure-10 (2) | 170715     | 7117         | 6392          | 168                     |
| BenchmarkCopy_Clone-10 (3)         | 638623     | 1888         | 1656          | 22                      |

(1) https://github.com/barkimedes/go-deepcopy (does not support unexported fields)

(2) https://github.com/mitchellh/copystructure (does not support cycles)

(3) https://github.com/huandu/go-clone
