module github.com/brunoga/deep/examples/incident

go 1.27

require github.com/brunoga/deep/v6 v6.3.0

// The replaces apply only when this module is built as the main module —
// developing in this repository. Consumers resolve the requires above.
replace github.com/brunoga/deep/v6 => ../../
