# deep
Support for doing deep copies of (almost all) Go types.

This is a from scratch implementation of the ideas from https://github.com/barkimedes/go-deepcopy (which, unfortunatelly appears to be dead) but it should be faster for most cases (*), simpler to use for callers and supports copying unexported fields.

It should support most Go types. Specificaly, it does not support functions, channels and unsafe.Pointers unless they are nil. Also it might have weird interations with structs that includes any synchronization primitives (mutexes, for example. They should still be copied but if they are usable after that is left as an exercise to the user).

(*) In my machine (8-Core Intel Atom NAS, running in a Linux conainer):

go-deepcopy:

`BenchmarkCopy-8 84931 21655 ns/op 1912 B/op 50 allocs/op`

deep:

`BenchmarkCopy-8 104366 17450 ns/op 1824 B/op 43 allocs/op`
