module github.com/brunoga/deep/examples/incident

go 1.27

require (
	github.com/brunoga/deep/proto v1.3.0
	github.com/brunoga/deep/v6 v6.3.0
	github.com/brunoga/deep/ws v1.1.0
	google.golang.org/protobuf v1.36.12
)

require github.com/coder/websocket v1.8.13 // indirect

// The replaces apply only when this module is built as the main module —
// developing in this repository. Consumers resolve the requires above.
replace (
	github.com/brunoga/deep/proto => ../../proto
	github.com/brunoga/deep/v6 => ../../
	github.com/brunoga/deep/ws => ../../ws
)
