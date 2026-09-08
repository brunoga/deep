module github.com/brunoga/deep/examples/editor

go 1.27

require (
	github.com/brunoga/deep/v6 v6.7.0
	github.com/brunoga/deep/ws v1.2.1
)

require github.com/coder/websocket v1.8.13 // indirect

// The replaces apply only when this module is built as the main module —
// developing in this repository. Consumers resolve the requires above.
replace (
	github.com/brunoga/deep/v6 => ../../
	github.com/brunoga/deep/ws => ../../ws
)
