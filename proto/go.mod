module github.com/brunoga/deep/proto

go 1.27

require (
	github.com/brunoga/deep/v6 v6.2.0
	google.golang.org/protobuf v1.36.6
)

// The replace applies only when this module is built as the main module —
// developing in this repository. Consumers resolve the require above.
replace github.com/brunoga/deep/v6 => ../
