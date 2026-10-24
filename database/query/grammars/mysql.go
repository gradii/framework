package grammars

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/goravel/framework/database/query/core"
)

// MySqlGrammar mirrors Illuminate\Database\Query\Grammars\MySqlGrammar. Only the methods whose SQL
// differs from the base are declared here.
type MySqlGrammar struct {
	*BaseGrammar
}

// NewMySqlGrammar creates the MySQL grammar, which quotes identifiers with backticks.
func NewMySqlGrammar(prefix string) *MySqlGrammar {
	g := &MySqlGrammar{BaseGrammar: NewBaseGrammar(prefix)}
	g.Attach(g)
	g.SetValueWrapper(func(value string) string {
		if value == "*" {
			return value
		}
		return "`" + strings.ReplaceAll(value, "`", "``") + "`"
	})

	return g
}

// GetOperators mirrors MySqlGrammar::$operators: "sounds like" is the one clause operator this
// dialect adds beyond the base set. Builder::invalidOperator() reads it back through getOperators(),
// so without it our Builder treats "sounds like" as unknown and shifts it into the value slot.
func (g *MySqlGrammar) GetOperators() []string {
	return []string{"sounds like"}
}

// WhereLike mirrors MySqlGrammar::whereLike().
func (g *MySqlGrammar) WhereLike(b core.Query, where core.Where) string {
	operator := ""
	if where.Not {
		operator = "not "
	}
	if where.CaseSensitive {
		operator += "like binary"
	} else {
		operator += "like"
	}

	basic := where
	basic.Operator = operator

	return g.WhereBasic(b, basic)
}

// WhereNull mirrors MySqlGrammar::whereNull().
func (g *MySqlGrammar) WhereNull(b core.Query, where core.Where) string {
	column := core.GetValueString(where.Column)
	if g.IsJsonSelector(column) {
		field, path := g.wrap.JsonFieldAndPath(column)

		return "(json_extract(" + field + path + ") is null OR json_type(json_extract(" + field + path + ")) = 'NULL')"
	}

	return g.BaseGrammar.WhereNull(b, where)
}

// WhereNotNull mirrors MySqlGrammar::whereNotNull().
func (g *MySqlGrammar) WhereNotNull(b core.Query, where core.Where) string {
	column := core.GetValueString(where.Column)
	if g.IsJsonSelector(column) {
		field, path := g.wrap.JsonFieldAndPath(column)

		return "(json_extract(" + field + path + ") is not null AND json_type(json_extract(" + field + path + ")) != 'NULL')"
	}

	return g.BaseGrammar.WhereNotNull(b, where)
}

// WhereFullText mirrors MySqlGrammar::whereFullText().
func (g *MySqlGrammar) WhereFullText(b core.Query, where core.Where) string {
	columns := g.Columnize(where.Columns)
	value := g.Parameter(where.Value)

	mode := " in natural language mode"
	if hasOption(where.Options, "mode", "boolean") {
		mode = " in boolean mode"
	}

	expanded := ""
	if hasOption(where.Options, "expanded", "") && !hasOption(where.Options, "mode", "boolean") {
		expanded = " with query expansion"
	}

	return "match (" + columns + ") against (" + value + mode + expanded + ")"
}

// CompileIndexHint mirrors MySqlGrammar::compileIndexHint().
func (g *MySqlGrammar) CompileIndexHint(b core.Query, hint *core.IndexHint) string {
	switch hint.Type {
	case "hint":
		return "use index (" + hint.Index + ")"
	case "force":
		return "force index (" + hint.Index + ")"
	default:
		return "ignore index (" + hint.Index + ")"
	}
}

// CompileInsert mirrors MySqlGrammar::compileInsert(): an empty insert becomes a default row.
func (g *MySqlGrammar) CompileInsert(b core.Query, values []map[string]any) string {
	if len(values) == 0 {
		values = []map[string]any{{}}
	}

	return g.BaseGrammar.CompileInsert(b, values)
}

// CompileInsertOrIgnore mirrors MySqlGrammar::compileInsertOrIgnore().
func (g *MySqlGrammar) CompileInsertOrIgnore(b core.Query, values []map[string]any) string {
	return strings.Replace(g.CompileInsert(b, values), "insert", "insert ignore", 1)
}

// CompileInsertOrIgnoreUsing mirrors MySqlGrammar::compileInsertOrIgnoreUsing().
func (g *MySqlGrammar) CompileInsertOrIgnoreUsing(b core.Query, columns []any, sql string) string {
	return strings.Replace(g.CompileInsertUsing(b, columns, sql), "insert", "insert ignore", 1)
}

// CompileUpdateColumns mirrors MySqlGrammar::compileUpdateColumns().
func (g *MySqlGrammar) CompileUpdateColumns(b core.Query, values core.UpdateValues) string {
	parts := make([]string, 0, len(values))

	for _, value := range values {
		if g.IsJsonSelector(value.Column) {
			parts = append(parts, g.CompileJsonUpdateColumn(value.Column, value.Value))
			continue
		}
		parts = append(parts, g.Wrap(value.Column)+" = "+g.Parameter(value.Value))
	}

	return strings.Join(parts, ", ")
}

// PrepareBindingsForUpdate mirrors MySqlGrammar::prepareBindingsForUpdate(): a JSON selector bound to a
// bool compiles to an inline true/false literal, so its binding is dropped; every array value is
// json-encoded. The rest keeps the base ordering.
func (g *MySqlGrammar) PrepareBindingsForUpdate(bindings map[string][]any, values core.UpdateValues) []any {
	prepared := make(core.UpdateValues, 0, len(values))

	for _, value := range values {
		if g.IsJsonSelector(value.Column) {
			if _, ok := value.Value.(bool); ok {
				continue
			}
		}

		if core.IsArrayValue(value.Value) {
			prepared = append(prepared, core.UpdateValue{Column: value.Column, Value: encodeJSON(value.Value)})
			continue
		}

		prepared = append(prepared, value)
	}

	return g.BaseGrammar.PrepareBindingsForUpdate(bindings, prepared)
}

// CompileUpsert mirrors MySqlGrammar::compileUpsert(): on duplicate key update.
//
// When the connection enables use_upsert_alias, the insert carries "as laravel_upsert_alias" and the
// copied columns read back through that alias instead of values(). An update entry that binds its own
// value (a string key in Laravel's $update) still compiles as "col = ?"; the alias only changes the
// value-copying form, exactly as Laravel's numeric-key branch decides.
func (g *MySqlGrammar) CompileUpsert(b core.Query, values []map[string]any, uniqueBy []string, update []core.UpsertUpdate) string {
	sql := g.CompileInsert(b, values)

	if b.State().UseUpsertAlias {
		sql += " as laravel_upsert_alias"
	}

	sql += " on duplicate key update "

	parts := make([]string, 0, len(update))
	for _, item := range update {
		if item.Bound {
			parts = append(parts, g.Wrap(item.Column)+" = "+g.Parameter(item.Value))
			continue
		}
		if b.State().UseUpsertAlias {
			parts = append(parts, g.Wrap(item.Column)+" = "+g.Wrap("laravel_upsert_alias")+"."+g.Wrap(item.Column))
			continue
		}
		parts = append(parts, g.Wrap(item.Column)+" = values("+g.Wrap(item.Column)+")")
	}

	return sql + strings.Join(parts, ", ")
}

// CompileUpdateWithoutJoins mirrors MySqlGrammar::compileUpdateWithoutJoins(): order by and limit.
func (g *MySqlGrammar) CompileUpdateWithoutJoins(b core.Query, table string, columns string, where string) string {
	sql := g.BaseGrammar.CompileUpdateWithoutJoins(b, table, columns, where)

	if len(b.State().Orders) > 0 {
		sql += " " + g.CompileOrders(b, b.State().Orders)
	}
	if b.State().Limit != nil {
		sql += " " + g.CompileLimit(b, b.State().Limit)
	}

	return sql
}

// CompileDeleteWithoutJoins mirrors MySqlGrammar::compileDeleteWithoutJoins(): order by and limit.
func (g *MySqlGrammar) CompileDeleteWithoutJoins(b core.Query, table string, where string) string {
	sql := g.BaseGrammar.CompileDeleteWithoutJoins(b, table, where)

	if len(b.State().Orders) > 0 {
		sql += " " + g.CompileOrders(b, b.State().Orders)
	}
	if b.State().Limit != nil {
		sql += " " + g.CompileLimit(b, b.State().Limit)
	}

	return sql
}

// CompileJoinLateral mirrors MySqlGrammar::compileJoinLateral().
func (g *MySqlGrammar) CompileJoinLateral(join core.Join, expression string) string {
	return strings.TrimSpace(join.JoinType() + " join lateral " + expression + " on true")
}

// The MySQL driver can connect to MariaDB. Detect its lateral-join limitation from
// the connection version even when the configured driver name is "mysql".
func (g *MySqlGrammar) CompileJoins(b core.Query, joins []core.Join) string {
	for _, join := range joins {
		if !join.IsLateral() || b.State().ServerVersion == nil {
			continue
		}
		version, err := b.State().ServerVersion()
		if err != nil {
			b.SetError(err)
			return ""
		}
		if strings.Contains(strings.ToLower(version), "mariadb") {
			b.SetError(errLateralJoinNotSupported)
			return ""
		}
	}
	return g.BaseGrammar.CompileJoins(b, joins)
}

// CompileLock mirrors MySqlGrammar::compileLock().
func (g *MySqlGrammar) CompileLock(b core.Query, value any) string {
	if s, ok := value.(string); ok {
		return s
	}

	if locked, ok := value.(bool); ok && locked {
		return "for update"
	}

	return "lock in share mode"
}

// CompileRandom mirrors MySqlGrammar::compileRandom().
func (g *MySqlGrammar) CompileRandom(seed any) string { return "RAND(" + core.AsString(seed) + ")" }

// CompileJsonValueCast mirrors MySqlGrammar::compileJsonValueCast().
func (g *MySqlGrammar) CompileJsonValueCast(value string) string {
	return "cast(" + value + " as json)"
}

// CompileJsonContains mirrors MySqlGrammar::compileJsonContains().
func (g *MySqlGrammar) CompileJsonContains(column string, value string) string {
	field, path := g.wrap.JsonFieldAndPath(column)

	return "json_contains(" + field + ", " + value + path + ")"
}

// CompileJsonContainsKey mirrors MySqlGrammar::compileJsonContainsKey().
func (g *MySqlGrammar) CompileJsonContainsKey(column string) string {
	field, path := g.wrap.JsonFieldAndPath(column)

	return "ifnull(json_contains_path(" + field + ", 'one'" + path + "), 0)"
}

// CompileJsonLength mirrors MySqlGrammar::compileJsonLength().
func (g *MySqlGrammar) CompileJsonLength(column string, operator string, value string) string {
	field, path := g.wrap.JsonFieldAndPath(column)

	return "json_length(" + field + path + ") " + operator + " " + value
}

// CompileJsonOverlaps mirrors MySqlGrammar::compileJsonOverlaps().
func (g *MySqlGrammar) CompileJsonOverlaps(column string, value string) string {
	field, path := g.wrap.JsonFieldAndPath(column)

	return "json_overlaps(" + field + ", " + value + path + ")"
}

// CompileJsonUpdateColumn mirrors MySqlGrammar::compileJsonUpdateColumn().
func (g *MySqlGrammar) CompileJsonUpdateColumn(key string, value any) string {
	var rendered string

	switch v := value.(type) {
	case bool:
		if v {
			rendered = "true"
		} else {
			rendered = "false"
		}
	case []any, map[string]any:
		rendered = "cast(? as json)"
	default:
		rendered = g.Parameter(value)
	}

	field, path := g.wrap.JsonFieldAndPath(key)

	return field + " = json_set(" + field + path + ", " + rendered + ")"
}

// WrapJsonSelector mirrors MySqlGrammar::wrapJsonSelector().
func (g *MySqlGrammar) WrapJsonSelector(value string) string {
	field, path := g.wrap.JsonFieldAndPath(value)

	return "json_unquote(json_extract(" + field + path + "))"
}

// WrapJsonBooleanSelector mirrors MySqlGrammar::wrapJsonBooleanSelector().
func (g *MySqlGrammar) WrapJsonBooleanSelector(value string) string {
	field, path := g.wrap.JsonFieldAndPath(value)

	return "json_extract(" + field + path + ")"
}

// UseLegacyGroupLimit follows the connection's server version, as in Laravel.
// SQL-only builders without a connection retain the modern compilation path.
func (g *MySqlGrammar) UseLegacyGroupLimit(b core.Query) bool {
	if b.State().ServerVersion == nil {
		return false
	}
	version, err := b.State().ServerVersion()
	if err != nil {
		b.SetError(err)
		return false
	}
	if strings.Contains(strings.ToLower(version), "mariadb") {
		return false
	}
	var major, minor, patch int
	_, _ = fmt.Sscanf(version, "%d.%d.%d", &major, &minor, &patch)
	return major < 8 || (major == 8 && minor == 0 && patch < 11)
}

// CompileGroupLimit mirrors MySqlGrammar::compileGroupLimit().
func (g *MySqlGrammar) CompileGroupLimit(b core.Query) string {
	if g.UseLegacyGroupLimit(b) {
		return g.CompileLegacyGroupLimit(b)
	}

	return g.BaseGrammar.CompileGroupLimit(b)
}

// CompileLegacyGroupLimit mirrors MySqlGrammar::compileLegacyGroupLimit().
func (g *MySqlGrammar) CompileLegacyGroupLimit(b core.Query) string {
	limit := b.State().GroupLimit
	offset := b.State().Offset
	if offset != nil {
		limit += *offset
		b.State().Offset = nil
	}

	column := b.State().GroupLimitColumn
	parts := strings.Split(column, ".")
	column = g.Wrap(parts[len(parts)-1])

	partition := ", @laravel_row := if(@laravel_group = " + column + ", @laravel_row + 1, 1) as `laravel_row`"
	partition += ", @laravel_group := " + column

	orders := append([]core.Order{{Column: b.State().GroupLimitColumn, Direction: "asc"}}, b.State().Orders...)
	b.State().Orders = orders

	components := g.CompileComponents(b)

	joined := make([]string, 0, len(components))
	for _, component := range components {
		if strings.HasPrefix(component, "order by ") {
			continue
		}
		joined = append(joined, component)
	}

	sql := strings.Join(joined, " ") + partition

	row := g.Wrap("laravel_row")
	sql = "select * from (" + sql + ") as " + g.Wrap("laravel_table") + " where " + row + " <= " + strconv.Itoa(limit)
	if offset != nil {
		sql += " and " + row + " > " + strconv.Itoa(*offset)
	}

	return sql + " order by " + row
}

// hasOption reports whether a fulltext option list contains key with the given value. Laravel
// reads these from an options array; Go carries them as key or key:value strings.
func hasOption(options []string, key string, value string) bool {
	for _, option := range options {
		if option == key {
			return value == ""
		}
		if strings.HasPrefix(option, key+":") {
			return strings.TrimPrefix(option, key+":") == value
		}
	}

	return false
}
