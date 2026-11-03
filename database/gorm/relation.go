package gorm

import (
	"reflect"

	"github.com/gradii/framework/database/gorm/relations"
	"github.com/gradii/framework/database/internal/annotation"
	"github.com/gradii/framework/database/orm/morphmap"
	"github.com/gradii/framework/errors"
)

// The relation descriptor — the resolver's normalised view of a declared relationship and the
// per-kind constructors behind it — lives in database/gorm/relations/descriptor.go since the S3
// reduction slice (database/tmp/LARAVEL_1TO1_ALIGNMENT_REVIEW.md §C.4). This file is the gorm-side
// half of that seam: the names this package's engines declare against (aliases of the one
// definition, not copies), the readers the resolver cannot perform from its own package
// (relations.DescriptorSeam — installed once at init) and the schema-bound helpers those readers
// wrap, which stay here because they are this package's model-metadata rules.

// The descriptor types under the names the eager loader, the existence compiler, the touch
// integration and the write shells declare against.
type (
	relationDescriptor = relations.Descriptor
	referenceKey       = relations.ReferenceKey
	relationKind       = relations.Kind
	// eagerLoadEntry is one entry of the eager-load list; the grammar producing it moved to
	// database/gorm/relations/eager_load_parse.go in the S4 slice.
	eagerLoadEntry = relations.EagerLoadEntry
)

// The relation kinds under the names the engines switch on — the relations package's Kind values,
// whose iota order is the one the descriptor always had.
const (
	relKindHasOne         = relations.KindHasOne
	relKindHasMany        = relations.KindHasMany
	relKindBelongsTo      = relations.KindBelongsTo
	relKindMany2Many      = relations.KindMany2Many
	relKindMorphOne       = relations.KindMorphOne
	relKindMorphMany      = relations.KindMorphMany
	relKindMorphTo        = relations.KindMorphTo
	relKindMorphToMany    = relations.KindMorphToMany
	relKindHasOneThrough  = relations.KindHasOneThrough
	relKindHasManyThrough = relations.KindHasManyThrough
)

// init installs the gorm-side readers the descriptor resolver runs on: the schema parser, the
// table resolution, the pivot-field reflection, the GORM relation-tag check and the morph map
// (database/orm/morphmap imports the relations package, so the resolver cannot import it).
func init() {
	relations.UseDescriptorSeam(relations.DescriptorSeam{
		ParseModel:           parseModelSchema,
		TableOf:              tableNameFor,
		PivotFieldStructType: pivotFieldStructType,
		HasRelationTag:       hasRelationTag,
		MorphValue:           morphmap.MorphValue,
	})
}

// parseModelSchema parses a model's schema and hands it to the resolver as the relations.ModelSchema
// view — parseSchema, errors included.
func parseModelSchema(model any) (relations.ModelSchema, error) {
	schema, err := parseSchema(model)
	if err != nil {
		return nil, err
	}

	return modelSchemaView{schema: schema}, nil
}

// modelSchemaView is the relations.ModelSchema view over a parsed modelSchema: the table, the Go
// type name, a column's Go type by database name, the columns in declaration order and a column by
// Go field name — the members the descriptor resolver reads.
type modelSchemaView struct {
	schema *modelSchema
}

func (v modelSchemaView) Table() string {
	return v.schema.Table
}

func (v modelSchemaView) TypeName() string {
	return v.schema.Name
}

func (v modelSchemaView) ColumnType(column string) (reflect.Type, bool) {
	field, ok := v.schema.FieldsByDBName[column]
	if !ok {
		return nil, false
	}

	return field.FieldType, true
}

func (v modelSchemaView) Columns() []relations.SchemaColumn {
	columns := make([]relations.SchemaColumn, 0, len(v.schema.Fields))
	for _, field := range v.schema.Fields {
		columns = append(columns, schemaColumnOf(field))
	}

	return columns
}

func (v modelSchemaView) ColumnByName(name string) (relations.SchemaColumn, bool) {
	field, ok := v.schema.FieldsByName[name]
	if !ok {
		return relations.SchemaColumn{}, false
	}

	return schemaColumnOf(field), true
}

// schemaColumnOf projects one schemaField onto the resolver's column view; the automatic
// timestamp flags are the "stamped" reading of AutoCreateTime / AutoUpdateTime (non-zero).
func schemaColumnOf(field *schemaField) relations.SchemaColumn {
	return relations.SchemaColumn{
		DBName:         field.DBName,
		FieldType:      field.FieldType,
		AutoCreateTime: field.AutoCreateTime != 0,
		AutoUpdateTime: field.AutoUpdateTime != 0,
	}
}

// hasRelationTag reports whether a model's field of the given name declares a GORM relation.
//
// The framework declares relations through Relations(), so a model that still carries one of GORM's
// relation tags is rejected rather than resolved by a mechanism the model's own declaration does not
// mention. Embedded structs are searched too, because GORM read their tags as if they were the outer
// model's own.
func hasRelationTag(model any, name string) bool {
	modelType := indirectStructType(reflect.TypeOf(model))
	if modelType == nil {
		return false
	}

	return typeHasRelationTag(modelType, name)
}

func typeHasRelationTag(modelType reflect.Type, name string) bool {
	for position := 0; position < modelType.NumField(); position++ {
		field := modelType.Field(position)
		if !field.IsExported() {
			continue
		}

		if field.Anonymous && !isColumnType(field.Type) {
			if embedded := indirectStructType(field.Type); embedded != nil && typeHasRelationTag(embedded, name) {
				return true
			}

			continue
		}

		if field.Name == name {
			return annotation.ParseAnnotation(field.Tag.Get("gorm")).Relation
		}
	}

	return false
}

// tableNameFor returns the table name for a model, without the connection prefix.
//
// The name is deliberately bare: every statement the ORM renders goes through the framework
// grammar, whose WrapTable applies the prefix to a table standing alone and whose Wrap applies it
// to the leading segment of a dotted table.column fragment. Prefixing here as well produced doubly
// prefixed names such as "goravel_goravel_users", which address no table.
//
// The name comes from the model's own declaration through TableName() (see orm.ModelWithTableName)
// and from nowhere else. There is no naming-strategy fallback left to silently pluralize a model
// that forgot to declare its table, so a model that declares none is reported as a named error
// naming its type and the method to add. The connection argument is kept only because every call
// site already passes one.
func tableNameFor(model any) (string, error) {
	if name := declaredTableName(model); name != "" {
		return name, nil
	}

	// A slice names the model it carries, so the error names that model rather than the slice: the
	// caller wrote the destination, and the method to add belongs on its element type.
	modelType := reflect.TypeOf(model)
	pointer := false
	for modelType != nil && modelType.Kind() == reflect.Pointer {
		pointer = true
		modelType = modelType.Elem()
	}
	if modelType != nil && (modelType.Kind() == reflect.Slice || modelType.Kind() == reflect.Array) {
		element := modelType.Elem()
		for element.Kind() == reflect.Slice || element.Kind() == reflect.Array {
			element = element.Elem()
		}
		if element.Kind() != reflect.Pointer {
			element = reflect.PointerTo(element)
		}
		modelType = element
	} else if pointer {
		modelType = reflect.PointerTo(modelType)
	}

	return "", errors.OrmQueryTableNotSet.Args(modelType)
}

// pivotFieldStructType reflects relatedModel for a struct field named pivotFieldName and returns
// its underlying struct type. Returns ok=false when the related model has no such field, or when
// the field exists but isn't a struct (the eager loader will surface the mismatched-kind error
// later via OrmRelationPivotFieldNotStruct; here we silently fall back to no struct-driven config).
func pivotFieldStructType(relatedModel any, pivotFieldName string) (reflect.Type, bool) {
	relatedType := reflect.TypeOf(relatedModel)
	if relatedType.Kind() == reflect.Pointer {
		relatedType = relatedType.Elem()
	}
	if relatedType.Kind() != reflect.Struct {
		return nil, false
	}
	field, ok := relatedType.FieldByName(pivotFieldName)
	if !ok || field.Type.Kind() != reflect.Struct {
		return nil, false
	}
	return field.Type, true
}
