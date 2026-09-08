module github.com/brunoga/deep/examples/fieldwork

go 1.27

// The replace applies only when this module is built as the main module —
// developing in this repository. Consumers resolve the require above.
replace github.com/brunoga/deep/v6 => ../../

require github.com/brunoga/deep/v6 v6.5.0
