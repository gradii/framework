package driver

// Schema is a database namespace. SQLite supplies its attached file as Path.
type Schema struct {
	Name    string  `json:"name"`
	Path    *string `json:"path"`
	Default bool    `json:"default"`
}
type ColumnGeneration struct {
	Type       string  `json:"type"`
	Expression *string `json:"expression"`
}

// ColumnMetadata retains Laravel's nullable metadata without changing the legacy Column fields.
type ColumnMetadata struct {
	Name          string            `json:"name"`
	Type          string            `json:"type"`
	TypeName      string            `json:"type_name"`
	Collation     *string           `json:"collation"`
	Nullable      bool              `json:"nullable"`
	Default       any               `json:"default"`
	AutoIncrement bool              `json:"auto_increment"`
	Comment       *string           `json:"comment"`
	Generation    *ColumnGeneration `json:"generation"`
	Extra         string            `json:"-"`
}
