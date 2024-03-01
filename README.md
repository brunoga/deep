# deep
Support for doing deep copies of (almost all) Go types.

This is a from scratch implementation of the ideas from https://github.com/barkimedes/go-deepcopy (which, unfortunatelly appears to be dead) but it should be faster for most cases, simpler to use for callers and supports copying unexported fields.

It should support most Go types. The main 2 that it does not support is functions (we can not really copy them) and channels. Also it might have weird interations with structs that includes any synchronization primitives (mutexes, for exasmple. They should still be copied but if they are usable after that is left as an exercise to the user).

I will continue working on this if I can find sane ways to deal with currently unsupported types.

