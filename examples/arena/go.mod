module github.com/brunoga/deep/examples/arena

go 1.27

// The replace applies only when this module is built as the main module —
// developing in this repository. Consumers resolve the require above.
replace github.com/brunoga/deep/v6 => ../../

require github.com/brunoga/deep/v6 v6.4.0

require github.com/coder/websocket v1.8.13 // indirect
