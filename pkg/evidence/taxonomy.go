package evidence

type Category struct {
	ID          string `json:"id"`
	Description string `json:"description"`
}
type Taxonomy struct {
	SchemaVersion string     `json:"schema_version"`
	Version       string     `json:"version"`
	Categories    []Category `json:"categories"`
	Facets        []string   `json:"facets"`
}

func TaxonomyV1() Taxonomy {
	return Taxonomy{SchemaVersion, "v1", []Category{
		{"code.fix", "a fix against a reproduction"}, {"code.implement", "new behaviour and feature code"},
		{"code.refactor", "behaviour-preserving change"}, {"code.test", "writing or repairing tests"},
		{"docs.write", "documentation and reference pages"}, {"ops", "CI, infrastructure and release chores"},
		{"orchestration", "coordinating child agents over a long horizon"}, {"planning", "decomposition, estimates and plans"},
		{"research", "investigation from sources and written findings"}, {"review.code", "reviewing a code change"},
		{"review.spec", "reviewing a specification or design"}, {"routine", "mechanical edits, formatting and translation"},
		{"tool-use", "agentic terminal and tool work"}}, []string{"context_size", "horizon", "language", "platform"}}
}
