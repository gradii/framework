package schema

import (
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/gradii/framework/contracts/database/driver"
	"github.com/gradii/framework/errors"
)

func (r *Schema) ddlWrap() *Wrap {
	wrap := NewWrap(r.prefix)
	switch r.metadataDialect() {
	case "mysql":
		wrap.SetValueWrapper(func(value string) string {
			if value == "*" {
				return value
			}
			return "`" + strings.ReplaceAll(value, "`", "``") + "`"
		})
	case "sqlserver":
		wrap.SetValueWrapper(func(value string) string {
			if value == "*" {
				return value
			}
			return "[" + strings.ReplaceAll(value, "]", "]]") + "]"
		})
	}
	return wrap
}
func ddlExpression(value any) (string, error) {
	switch value := value.(type) {
	case nil:
		return "", nil
	case string:
		return value, nil
	case Expression:
		return string(value), nil
	case interface{ SQL() string }:
		return value.SQL(), nil
	case interface{ GetValue() string }:
		return value.GetValue(), nil
	default:
		return "", errors.DatabaseUnsupportedType.Args(fmt.Sprintf("%T", value), "SQL string or expression")
	}
}
func legacyModifier(grammar driver.Grammar, name string, blueprint *Blueprint, column *ColumnDefinition) string {
	method := reflect.ValueOf(grammar).MethodByName("Modify" + name)
	if !method.IsValid() {
		return ""
	}
	values := method.Call([]reflect.Value{reflect.ValueOf(blueprint), reflect.ValueOf(column)})
	if len(values) == 0 {
		return ""
	}
	return values[0].String()
}
func (r *Schema) ddlType(grammar driver.Grammar, column *ColumnDefinition) (string, error) {
	dialect := r.metadataDialect()
	switch column.GetType() {
	case "raw":
		return column.rawDefinition, nil
	case "binary":
		switch dialect {
		case "postgres":
			return "bytea", nil
		case "sqlite":
			return "blob", nil
		default:
			if column.GetLength() > 0 {
				kind := "varbinary"
				if column.fixed {
					kind = "binary"
				}
				return fmt.Sprintf("%s(%d)", kind, column.GetLength()), nil
			}
			if dialect == "mysql" {
				return "blob", nil
			}
			return "varbinary(max)", nil
		}
	case "year":
		if dialect == "mysql" {
			return "year", nil
		}
		return "integer", nil
	case "ipAddress":
		if dialect == "postgres" {
			return "inet", nil
		}
		if dialect == "sqlserver" {
			return "nvarchar(45)", nil
		}
		return "varchar(45)", nil
	case "macAddress":
		if dialect == "postgres" {
			return "macaddr", nil
		}
		if dialect == "sqlserver" {
			return "nvarchar(17)", nil
		}
		return "varchar(17)", nil
	case "set":
		if dialect != "mysql" {
			return "", errors.SchemaFeatureNotSupported.Args(r.driverName, "set columns")
		}
		values := make([]string, len(column.allowed))
		for i, value := range column.allowed {
			values[i] = ColumnDefaultValue(value)
		}
		return "set(" + strings.Join(values, ", ") + ")", nil
	case "vector":
		if dialect != "mysql" && dialect != "postgres" {
			return "", errors.SchemaFeatureNotSupported.Args(r.driverName, "vector columns")
		}
		if column.dimensions != nil {
			return fmt.Sprintf("vector(%d)", *column.dimensions), nil
		}
		return "vector", nil
	case "computed":
		if dialect != "sqlserver" {
			return "", errors.SchemaComputedRequiresType
		}
		expression, err := ddlExpression(column.expression)
		return "as (" + expression + ")", err
	case "geometry", "geography":
		kind := column.GetType()
		subtype := strings.ToLower(column.subtype)
		srid := 0
		if column.srid != nil {
			srid = *column.srid
		}
		switch dialect {
		case "postgres":
			if subtype != "" {
				if srid != 0 {
					return fmt.Sprintf("%s(%s,%d)", kind, subtype, srid), nil
				}
				return kind + "(" + subtype + ")", nil
			}
			return kind, nil
		case "sqlserver":
			return kind, nil
		case "sqlite":
			return "geometry", nil
		case "mysql":
			allowed := []string{"point", "linestring", "polygon", "geometrycollection", "multipoint", "multilinestring", "multipolygon"}
			if !slices.Contains(allowed, subtype) {
				subtype = "geometry"
			}
			if srid != 0 {
				maria := strings.Contains(strings.ToLower(r.driverName), "maria")
				if !maria && r.db != nil {
					version, err := r.metadataServerVersion()
					if err != nil {
						return "", err
					}
					maria = strings.Contains(strings.ToLower(version), "mariadb")
				}
				if maria {
					return fmt.Sprintf("%s ref_system_id=%d", subtype, srid), nil
				}
				return fmt.Sprintf("%s srid %d", subtype, srid), nil
			}
			return subtype, nil
		}
	}
	return ColumnType(grammar, column), nil
}

func (r *Schema) ddlColumn(grammar driver.Grammar, blueprint *Blueprint, column *ColumnDefinition) (string, error) {
	kind, err := r.ddlType(grammar, column)
	if err != nil {
		return "", err
	}
	dialect := r.metadataDialect()
	sql := r.ddlWrap().Value(column.GetName()) + " " + kind
	orders := map[string][]string{
		"mysql":     {"Unsigned", "Charset", "Collate", "VirtualAs", "StoredAs", "Nullable", "Default", "OnUpdate", "Invisible", "Increment", "Comment", "After", "First"},
		"postgres":  {"Collate", "Nullable", "Default", "VirtualAs", "StoredAs", "GeneratedAs", "Increment"},
		"sqlite":    {"Nullable", "Default", "VirtualAs", "StoredAs", "Increment", "Collate"},
		"sqlserver": {"Collate", "Nullable", "Default", "Persisted", "Increment"},
	}
	for _, modifier := range orders[dialect] {
		switch modifier {
		case "Charset":
			if column.charset != "" {
				sql += " character set " + column.charset
			}
		case "Collate":
			if column.collation != "" {
				switch dialect {
				case "postgres":
					sql += " collate " + r.ddlWrap().Value(column.collation)
				case "sqlserver":
					sql += " collate " + column.collation
				default:
					sql += " collate " + ColumnDefaultValue(column.collation)
				}
			}
		case "VirtualAs", "StoredAs":
			value := column.virtualAs
			if modifier == "StoredAs" {
				value = column.storedAs
			}
			if value == nil {
				continue
			}
			expression, err := ddlExpression(value)
			if err != nil {
				return "", err
			}
			if dialect == "postgres" {
				sql += " generated always as (" + expression + ")"
				if modifier == "StoredAs" {
					sql += " stored"
				} else {
					sql += " virtual"
				}
			} else {
				sql += " as (" + expression + ")"
				if modifier == "StoredAs" {
					sql += " stored"
				}
			}
		case "Nullable":
			if dialect == "sqlserver" && column.GetType() == "computed" {
				continue
			}
			if (dialect == "mysql" || dialect == "sqlite") && (column.virtualAs != nil || column.storedAs != nil) {
				if column.nullable != nil && !*column.nullable {
					sql += " not null"
				}
			} else if dialect == "sqlite" {
				if !column.GetNullable() {
					sql += " not null"
				}
			} else {
				sql += legacyModifier(grammar, modifier, blueprint, column)
			}
		case "Invisible":
			if column.invisible {
				sql += " invisible"
			}
		case "Persisted":
			if column.persisted && column.GetType() == "computed" {
				sql += " persisted"
			}
		default:
			sql += legacyModifier(grammar, modifier, blueprint, column)
		}
	}
	return sql, nil
}
func (r *Schema) ddlTable(blueprint *Blueprint) string {
	if blueprint.temporary && r.metadataDialect() == "sqlserver" {
		return r.ddlWrap().Value("#" + r.prefix + blueprint.table)
	}
	return r.ddlWrap().Table(blueprint.table)
}
func (r *Schema) ddlCreate(blueprint *Blueprint, grammar driver.Grammar) (string, error) {
	columns := make([]string, 0, len(blueprint.columns))
	for _, column := range blueprint.columns {
		sql, err := r.ddlColumn(grammar, blueprint, column)
		if err != nil {
			return "", err
		}
		columns = append(columns, sql)
	}
	dialect := r.metadataDialect()
	for _, command := range blueprint.commands {
		switch command.Name {
		case CommandPrimary:
			if dialect == "mysql" || dialect == "sqlite" {
				algorithm := ""
				if command.Algorithm != "" && dialect == "mysql" {
					algorithm = "using " + command.Algorithm + " "
				}
				columns = append(columns, "primary key "+algorithm+"("+r.ddlWrap().Columnize(command.Columns)+")")
				command.ShouldBeSkipped = true
			}
		case CommandForeign:
			if dialect == "sqlite" {
				columns = append(columns, r.sqliteForeignDefinition(command))
				command.ShouldBeSkipped = true
			}
		}
	}
	create := "create"
	if blueprint.temporary && dialect != "sqlserver" {
		create += " temporary"
	}
	sql := create + " table " + r.ddlTable(blueprint) + " (" + strings.Join(columns, ", ") + ")"
	if dialect == "mysql" {
		if blueprint.charset != "" {
			sql += " default character set " + blueprint.charset
		}
		if blueprint.collation != "" {
			sql += " collate " + ColumnDefaultValue(blueprint.collation)
		}
		if blueprint.engine != "" {
			sql += " engine = " + blueprint.engine
		}
	}
	return sql, nil
}
func (r *Schema) sqliteForeignDefinition(command *driver.Command) string {
	sql := "foreign key(" + r.ddlWrap().Columnize(command.Columns) + ") references " + r.ddlWrap().Table(command.On) + "(" + r.ddlWrap().Columnize(command.References) + ")"
	if command.OnDelete != "" {
		sql += " on delete " + command.OnDelete
	}
	if command.OnUpdate != "" {
		sql += " on update " + command.OnUpdate
	}
	return sql
}
func (r *Schema) compileDDLCommand(blueprint *Blueprint, grammar driver.Grammar, command *driver.Command) ([]string, bool, error) {
	table := r.ddlTable(blueprint)
	dialect := r.metadataDialect()
	switch command.Name {
	case CommandCreate:
		if !blueprint.hasAdvanced() {
			return nil, false, nil
		}
		statement, err := r.ddlCreate(blueprint, grammar)
		return []string{statement}, true, err
	case CommandAdd:
		column, ok := command.Column.(*ColumnDefinition)
		if !ok || !column.advanced() {
			return nil, false, nil
		}
		if column.change {
			switch dialect {
			case "postgres":
				if (column.storedSet && column.storedAs != nil) || (column.virtualSet && column.virtualAs != nil) {
					return nil, true, errors.SchemaGeneratedColumnChange
				}
				kind, err := r.ddlType(grammar, column)
				if err != nil {
					return nil, true, err
				}
				if column.collation != "" {
					kind += " collate " + r.ddlWrap().Value(column.collation)
				}
				changes := []string{"type " + kind}
				for _, name := range []string{"Nullable", "Default", "GeneratedAsForChange", "GeneratedAs"} {
					if value := strings.TrimSpace(legacyModifier(grammar, name, blueprint, column)); value != "" {
						changes = append(changes, value)
					}
				}
				if column.storedSet || column.virtualSet {
					changes = append(changes, "drop expression if exists")
				}
				for i := range changes {
					changes[i] = "alter column " + r.ddlWrap().Value(column.GetName()) + " " + changes[i]
				}
				return []string{"alter table " + table + " " + strings.Join(changes, ", ")}, true, nil
			case "sqlserver":
				if column.GetType() == "computed" {
					action := "drop"
					if column.persisted {
						action = "add"
					}
					return []string{"alter table " + table + " alter column " + r.ddlWrap().Value(column.GetName()) + " " + action + " persisted"}, true, nil
				}
				statements := grammar.CompileChange(blueprint, command)
				definition, err := r.ddlColumn(grammar, blueprint, column)
				if err != nil {
					return nil, true, err
				}
				if len(statements) > 0 {
					statements = statements[:len(statements)-1]
				}
				return append(statements, "alter table "+table+" alter column "+definition), true, nil
			case "sqlite":
				return nil, true, errors.SchemaSQLiteRebuildRequired
			}
		}
		definition, err := r.ddlColumn(grammar, blueprint, column)
		if err != nil {
			return nil, true, err
		}
		action := "add "
		if column.change {
			action = "modify "
		} else if dialect == "postgres" || dialect == "sqlite" {
			action = "add column "
		}
		statement := "alter table " + table + " " + action + definition
		if column.instant && dialect == "mysql" {
			statement += ", algorithm=instant"
		}
		return []string{statement}, true, nil
	case "spatialIndex":
		index := r.ddlWrap().Value(command.Index)
		columns := r.ddlWrap().Columnize(command.Columns)
		switch dialect {
		case "mysql":
			return []string{"alter table " + table + " add spatial " + index + "(" + columns + ")"}, true, nil
		case "postgres":
			return []string{"create index " + index + " on " + table + " using gist (" + columns + ")"}, true, nil
		case "sqlserver":
			return []string{"create spatial index " + index + " on " + table + " (" + columns + ")"}, true, nil
		default:
			return nil, true, errors.SchemaFeatureNotSupported.Args(r.driverName, "spatial indexes")
		}
	case "dropSpatialIndex":
		switch dialect {
		case "mysql":
			return []string{"alter table " + table + " drop index " + r.ddlWrap().Value(command.Index)}, true, nil
		case "postgres":
			return []string{"drop index " + r.ddlWrap().Value(command.Index)}, true, nil
		case "sqlserver":
			return []string{"drop index " + r.ddlWrap().Value(command.Index) + " on " + table}, true, nil
		default:
			return nil, true, errors.SchemaFeatureNotSupported.Args(r.driverName, "spatial indexes")
		}
	case "rawIndex":
		index := r.ddlWrap().Value(command.Index)
		if dialect == "mysql" {
			return []string{"alter table " + table + " add index " + index + "(" + command.Value + ")"}, true, nil
		}
		return []string{"create index " + index + " on " + table + " (" + command.Value + ")"}, true, nil
	}
	return nil, false, nil
}
func (r *Schema) ddlStartingValues(blueprint *Blueprint) []string {
	var statements []string
	for _, column := range blueprint.columns {
		if column.startingValue == nil || !column.GetAutoIncrement() || *column.startingValue == 0 {
			continue
		}
		value := strconv.FormatUint(*column.startingValue, 10)
		switch r.metadataDialect() {
		case "mysql":
			statements = append(statements, "alter table "+r.ddlTable(blueprint)+" auto_increment = "+value)
		case "postgres":
			schema, table, err := r.metadataTable(blueprint.table)
			if err != nil {
				continue
			}
			sequence := table + "_" + column.GetName() + "_seq"
			if schema != "" {
				sequence = schema + "." + sequence
			}
			statements = append(statements, "alter sequence "+NewWrap("").Table(sequence)+" restart with "+value)
		}
	}
	return statements
}
