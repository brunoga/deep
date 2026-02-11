package deep

type ignorePathOption string

func (o ignorePathOption) applyDiff(c *diffConfig) {
	c.ignoredPaths[string(o)] = true
}

func (o ignorePathOption) applyCopy(c *copyConfig) {
	c.ignoredPaths[string(o)] = true
}

// IgnorePath returns an option that tells both Diff and Copy to ignore
// changes at the specified path.
// The path should use Go-style notation (e.g., "Field.SubField", "Map.Key", "Slice[0]").
func IgnorePath(path string) interface {
	DiffOption
	CopyOption
} {
	return ignorePathOption(path)
}
