module github.com/brunoga/deep/ws

go 1.27

require (
	github.com/brunoga/deep/v6 v6.2.0
	github.com/coder/websocket v1.8.13
)

// The replace applies only when this module is built as the main module —
// developing in this repository. Consumers resolve the require above.
replace github.com/brunoga/deep/v6 => ../
