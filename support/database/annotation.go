package database

import "strings"

// Annotation is the subset of a field's `gorm` struct tag that the framework reads itself.
//
// GORM's tags stay the mapping mechanism, but the framework only relies on the annotations that
// describe a schema: which column a field maps to, whether it is the primary key, whether it
// carries an index, and whether it is one of the automatic timestamps.
//
// Everything else a GORM tag can express — column types, sizes, default values, serializers, index
// classes and index options — is either GORM's own concern or belongs to the migration layer, which
// is implemented on Blueprint and does not read these tags.
type Annotation struct {
	// Column is the explicit column name, empty when the tag does not name one.
	Column string
	// PrimaryKey marks the model's primary key.
	PrimaryKey bool
	// Index marks a plain index on the field.
	Index bool
	// IndexName is the name the index tag gives, empty when the tag names none.
	IndexName string
	// UniqueIndex marks a unique index on the field.
	UniqueIndex bool
	// UniqueIndexName is the name the unique index tag gives, empty when the tag names none.
	UniqueIndexName string
	// AutoCreateTime marks the field stamped when a row is inserted.
	AutoCreateTime bool
	// AutoUpdateTime marks the field stamped when a row is inserted or updated.
	AutoUpdateTime bool
	// Relation reports whether the field declares a GORM relation. The framework declares relations
	// through a model's Relations() method instead, so a field carrying one of GORM's relation tags is
	// rejected rather than silently ignored.
	Relation bool
	// Skip reports whether the field is excluded from persistence entirely.
	Skip bool
	// SkipMigration reports a field that is persisted but excluded from migration.
	SkipMigration bool
}

// ParseAnnotation reads the annotations the framework honours out of a `gorm` struct tag.
//
// An empty tag yields the zero Annotation, so a field without one maps by convention. Unknown keys
// are ignored rather than rejected: a tag may carry settings this layer deliberately does not
// understand, and parse errors would turn a harmless tag into a broken model.
func ParseAnnotation(tag string) Annotation {
	var annotation Annotation

	for _, item := range strings.Split(tag, ";") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}

		key, value, _ := strings.Cut(item, ":")
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "column":
			annotation.Column = strings.TrimSpace(value)
		case "primarykey":
			annotation.PrimaryKey = true
		case "index":
			annotation.Index = true
			annotation.IndexName = indexName(value)
		case "uniqueindex":
			annotation.UniqueIndex = true
			annotation.UniqueIndexName = indexName(value)

		case "autocreatetime":
			annotation.AutoCreateTime = true
		case "autoupdatetime":
			annotation.AutoUpdateTime = true
		case "foreignkey", "references", "many2many", "polymorphic", "polymorphicvalue",
			"joinforeignkey", "joinreferences", "constraint":
			annotation.Relation = true
		case "-":
			// "-" excludes the field; "-:migration" keeps the column but leaves it out of
			// migrations, which the migration layer acts on.
			if strings.EqualFold(strings.TrimSpace(value), "migration") {
				annotation.SkipMigration = true
			} else {
				annotation.Skip = true
			}
		}
	}

	return annotation
}

// indexName extracts the name from an index tag value, which also carries comma separated
// per-index settings such as "priority:1". An empty value means the index is unnamed.
func indexName(value string) string {
	name, _, _ := strings.Cut(strings.TrimSpace(value), ",")

	return strings.TrimSpace(name)
}
