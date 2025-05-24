package patch

// OperationType defines the allowed JSON Patch operation types.
type OperationType string

const (
	OperationTypeAdd     OperationType = "add"
	OperationTypeRemove  OperationType = "remove"
	OperationTypeReplace OperationType = "replace"
	OperationTypeMove    OperationType = "move"
	OperationTypeCopy    OperationType = "copy"
	OperationTypeTest    OperationType = "test"
)

// Operation represents a single operation in a Patch.
type Operation struct {
	Op    OperationType `json:"op"`
	Path  string        `json:"path"`
	Value any           `json:"value,omitempty"` // Used for "add", "replace", "test"
	From  string        `json:"from,omitempty"`  // Used for "move", "copy"
}
