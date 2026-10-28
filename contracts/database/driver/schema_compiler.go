package driver

// SchemaStatement keeps values separate from SQL throughout schema execution.
type SchemaStatement struct {
	SQL      string
	Bindings []any
	// ReadOnly statements validate schema objects without executing a write.
	ReadOnly bool
}

// SchemaCompiler extends the legacy grammar without changing existing drivers.
// Unhandled commands use SchemaGrammar's legacy compilation methods.
type SchemaCompiler interface {
	CompileSchemaCommand(Blueprint, *Command) ([]SchemaStatement, bool, error)
	CompileSchemaStartingValues(Blueprint) ([]SchemaStatement, error)
}

type SchemaMetadataQuery struct {
	Kind, Schema, Table string
	Schemas             []string
}

type SchemaMetadataGrammar interface {
	CompileSchemaMetadata(SchemaMetadataQuery) (SchemaStatement, error)
	CurrentSchemas(database string) []string
}

type SchemaOperation struct {
	Kind, Name, Charset, Collation string
	Enabled                        bool
	Tables                         []Table
	Views                          []View
	Types                          []Type
}

// SchemaOperationsGrammar supplies SQL; leasing sessions and restoring their
// original constraint state remain the schema manager's responsibility.
type SchemaOperationsGrammar interface {
	CompileSchemaOperation(SchemaOperation) ([]SchemaStatement, error)
	ForeignKeyStateQuery() string
}

type BlueprintDescriber interface{ DescribeBlueprint() BlueprintDescription }
type ColumnDescriber interface{ DescribeColumn() ColumnDescription }

// Descriptions expose optional settings without coupling drivers to private
// blueprint fields. Pointer values distinguish an omitted option from zero.
type BlueprintDescription struct {
	Table, Prefix, Engine, Charset, Collation string
	Temporary                                 bool
	Columns                                   []ColumnDefinition
	Commands                                  []*Command
}

type ColumnDescription struct {
	Name, Type, After, Charset, Collation, Subtype, RawDefinition     string
	StoredAs, VirtualAs, Expression, Default, OnUpdate                any
	Allowed                                                           []any
	StoredSet, VirtualSet, Persisted, Invisible, Fixed, Instant       bool
	Always, Change, First                                             bool
	AutoIncrement, Nullable, Unsigned, UseCurrent, UseCurrentOnUpdate *bool
	Comment, GeneratedAs                                              *string
	Length, Places, Precision, Total, Srid, Dimensions                *int
	StartingValue                                                     *uint64
}

// SchemaGrammarWithVersionResolver binds a grammar instance to its own managed
// connection, including schema connections selected after construction.
type SchemaGrammarWithVersionResolver interface {
	SetVersionResolver(func() (string, error))
}

// SchemaCatalog exposes read-only access on the schema manager's active session.
// Dialects compile catalog queries; connection ownership stays in the framework.
type SchemaCatalog interface {
	Select(SchemaStatement) ([]map[string]any, error)
	Columns(string) ([]ColumnMetadata, error)
	ResolveTable(string) (string, string, error)
}

type SchemaExecutionPolicy struct {
	Transactional                 bool
	NestedTransaction             bool
	DisableForeignKeys            bool
	ForeignKeysOutsideTransaction bool
	SessionSettings               []SchemaSessionSetting
}

// SchemaSessionSetting describes a reversible setting on the leased writer.
// The framework reads its original value and restores it even after cancellation.
type SchemaSessionSetting struct {
	Read    SchemaStatement
	Value   any
	Compile func(any) SchemaStatement
}

type SchemaBlueprintPlanner interface {
	SchemaExecutionPolicy(Blueprint) SchemaExecutionPolicy
	CompileSchemaBlueprint(Blueprint, SchemaCatalog) ([]SchemaStatement, bool, error)
}

type SchemaOperationPlanner interface {
	SchemaOperationPolicy(SchemaOperation) SchemaExecutionPolicy
}

type SchemaMetadataResult struct {
	Rows      []map[string]any
	ColumnSQL []string
}

type SchemaMetadataResolver interface {
	ResolveSchemaMetadata(SchemaMetadataQuery, SchemaCatalog) (SchemaMetadataResult, error)
}

type SchemaConnectionInfo struct {
	Database, Username, Schema, Prefix string
	SearchPath                         any
}

type SchemaCurrentSchemas interface {
	ResolveCurrentSchemas(SchemaConnectionInfo, SchemaCatalog) ([]string, error)
}

// SchemaDatabaseFiles is implemented by databases whose database operations
// create or remove local files rather than execute SQL.
type SchemaDatabaseFiles interface {
	CreateDatabase(string) error
	DropDatabaseIfExists(string) error
}
