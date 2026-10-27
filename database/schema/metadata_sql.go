package schema

import (
	"fmt"
	"strings"

	"github.com/gradii/framework/errors"
)

func schemaPredicate(column string, schemas []string, fallback string) (string, []any) {
	if len(schemas) == 0 || (len(schemas) == 1 && schemas[0] == "") {
		return fallback, nil
	}
	values := make([]any, len(schemas))
	for i, schema := range schemas {
		values[i] = schema
	}
	if len(schemas) == 1 {
		return column + " = ?", values
	}
	return column + " in (" + strings.TrimSuffix(strings.Repeat("?, ", len(schemas)), ", ") + ")", values
}
func (r *Schema) metadataDialect() string {
	name := strings.NewReplacer(" ", "", "_", "", "-", "").Replace(strings.ToLower(r.driverName))
	switch name {
	case "mariadb":
		return "mysql"
	case "postgresql", "pgsql", "pgx":
		return "postgres"
	case "sqlite3":
		return "sqlite"
	case "mssql", "sqlsrv":
		return "sqlserver"
	default:
		return name
	}
}
func sqliteSchemaName(schema string) string {
	if schema == "" {
		return "main"
	}
	return schema
}
func quoteSchemaIdentifier(name string) string {
	return "\"" + strings.ReplaceAll(name, "\"", "\"\"") + "\""
}

// Catalog values are bound separately; identifier quoting is only needed for SQLite attachments.
func (r *Schema) metadataSQL(kind, schema, table string, schemas []string) (string, []any, error) {
	dialect := r.metadataDialect()
	switch kind {
	case "schemas":
		switch dialect {
		case "mysql":
			return "select schema_name as name, schema_name = schema() as `default` from information_schema.schemata where schema_name not in ('information_schema', 'mysql', 'ndbinfo', 'performance_schema', 'sys') order by schema_name", nil, nil
		case "postgres":
			return "select nspname as name, nspname = current_schema() as \"default\" from pg_namespace where nspname <> 'information_schema' and nspname not like 'pg\\_%' order by nspname", nil, nil
		case "sqlite":
			return "select name, file as path, name = 'main' as \"default\" from pragma_database_list order by name", nil, nil
		case "sqlserver":
			return "select name, iif(schema_id = schema_id(), 1, 0) as [default] from sys.schemas where name not in ('information_schema', 'sys') and name not like 'db[_]%' order by name", nil, nil
		}
	case "columns":
		switch dialect {
		case "mysql":
			return "select column_name as `name`, data_type as `type_name`, column_type as `type`, collation_name as `collation`, is_nullable as `nullable`, column_default as `default`, column_comment as `comment`, generation_expression as `expression`, extra as `extra` from information_schema.columns where table_schema = coalesce(nullif(?, ''), schema()) and table_name = ? order by ordinal_position asc", []any{schema, table}, nil
		case "postgres":
			version, err := r.metadataServerVersion()
			if err != nil {
				return "", nil, err
			}
			generated := "a.attgenerated as generated, "
			var major int
			_, _ = fmt.Sscanf(version, "%d", &major)
			if major < 12 {
				generated = "'' as generated, "
			}
			return "select a.attname as name, t.typname as type_name, format_type(a.atttypid, a.atttypmod) as type, (select tc.collcollate from pg_catalog.pg_collation tc where tc.oid = a.attcollation) as collation, not a.attnotnull as nullable, (select pg_get_expr(adbin, adrelid) from pg_attrdef where c.oid = pg_attrdef.adrelid and pg_attrdef.adnum = a.attnum) as default, " + generated + "col_description(c.oid, a.attnum) as comment from pg_attribute a, pg_class c, pg_type t, pg_namespace n where c.relname = ? and n.nspname = coalesce(nullif(?, ''), current_schema()) and a.attnum > 0 and a.attrelid = c.oid and a.atttypid = t.oid and n.oid = c.relnamespace order by a.attnum", []any{table, schema}, nil
		case "sqlite":
			return "select name, type, not \"notnull\" as \"nullable\", dflt_value as \"default\", pk as \"primary\", hidden as \"extra\" from pragma_table_xinfo(?, ?) order by cid asc", []any{table, sqliteSchemaName(schema)}, nil
		case "sqlserver":
			return "select col.name, type.name as type_name, col.max_length as length, col.precision as precision, col.scale as places, col.is_nullable as nullable, def.definition as [default], col.is_identity as autoincrement, col.collation_name as collation, com.definition as [expression], is_persisted as [persisted], cast(prop.value as nvarchar(max)) as comment from sys.columns as col join sys.types as type on col.user_type_id = type.user_type_id join sys.objects as obj on col.object_id = obj.object_id join sys.schemas as scm on obj.schema_id = scm.schema_id left join sys.default_constraints def on col.default_object_id = def.object_id and col.object_id = def.parent_object_id left join sys.extended_properties as prop on obj.object_id = prop.major_id and col.column_id = prop.minor_id and prop.name = 'MS_Description' left join sys.computed_columns as com on col.column_id = com.column_id and col.object_id = com.object_id where obj.type in ('U', 'V') and obj.name = ? and scm.name = coalesce(nullif(?, ''), schema_name()) order by col.column_id", []any{table, schema}, nil
		}
	case "indexes":
		switch dialect {
		case "mysql":
			return "select index_name as `name`, group_concat(column_name order by seq_in_index) as `columns`, index_type as `type`, not non_unique as `unique` from information_schema.statistics where table_schema = coalesce(nullif(?, ''), schema()) and table_name = ? group by index_name, index_type, non_unique", []any{schema, table}, nil
		case "postgres":
			return "select ic.relname as name, string_agg(a.attname, ',' order by indseq.ord) as columns, am.amname as \"type\", i.indisunique as \"unique\", i.indisprimary as \"primary\" from pg_index i join pg_class tc on tc.oid = i.indrelid join pg_namespace tn on tn.oid = tc.relnamespace join pg_class ic on ic.oid = i.indexrelid join pg_am am on am.oid = ic.relam join lateral unnest(i.indkey) with ordinality as indseq(num, ord) on true left join pg_attribute a on a.attrelid = i.indrelid and a.attnum = indseq.num where tc.relname = ? and tn.nspname = coalesce(nullif(?, ''), current_schema()) group by ic.relname, am.amname, i.indisunique, i.indisprimary", []any{table, schema}, nil
		case "sqlite":
			schema = sqliteSchemaName(schema)
			return "select 'primary' as name, group_concat(col) as columns, 1 as \"unique\", 1 as \"primary\" from (select name as col from pragma_table_xinfo(?, ?) where pk > 0 order by pk, cid) group by name union select name, group_concat(col) as columns, \"unique\", origin = 'pk' as \"primary\" from (select il.*, ii.name as col from pragma_index_list(?, ?) il, pragma_index_info(il.name, ?) ii order by il.seq, ii.seqno) group by name, \"unique\", \"primary\"", []any{table, schema, table, schema, schema}, nil
		case "sqlserver":
			return "select idx.name as name, string_agg(col.name, ',') within group (order by idxcol.key_ordinal) as columns, idx.type_desc as [type], idx.is_unique as [unique], idx.is_primary_key as [primary] from sys.indexes as idx join sys.tables as tbl on idx.object_id = tbl.object_id join sys.schemas as scm on tbl.schema_id = scm.schema_id join sys.index_columns as idxcol on idx.object_id = idxcol.object_id and idx.index_id = idxcol.index_id join sys.columns as col on idxcol.object_id = col.object_id and idxcol.column_id = col.column_id where tbl.name = ? and scm.name = coalesce(nullif(?, ''), schema_name()) group by idx.name, idx.type_desc, idx.is_unique, idx.is_primary_key", []any{table, schema}, nil
		}
	case "foreignKeys":
		switch dialect {
		case "mysql":
			return "select kc.constraint_name as `name`, group_concat(kc.column_name order by kc.ordinal_position) as `columns`, kc.referenced_table_schema as `foreign_schema`, kc.referenced_table_name as `foreign_table`, group_concat(kc.referenced_column_name order by kc.ordinal_position) as `foreign_columns`, rc.update_rule as `on_update`, rc.delete_rule as `on_delete` from information_schema.key_column_usage kc join information_schema.referential_constraints rc on kc.constraint_schema = rc.constraint_schema and kc.constraint_name = rc.constraint_name where kc.table_schema = coalesce(nullif(?, ''), schema()) and kc.table_name = ? and kc.referenced_table_name is not null group by kc.constraint_name, kc.referenced_table_schema, kc.referenced_table_name, rc.update_rule, rc.delete_rule", []any{schema, table}, nil
		case "postgres":
			return "select c.conname as name, string_agg(la.attname, ',' order by conseq.ord) as columns, fn.nspname as foreign_schema, fc.relname as foreign_table, string_agg(fa.attname, ',' order by conseq.ord) as foreign_columns, c.confupdtype as on_update, c.confdeltype as on_delete from pg_constraint c join pg_class tc on c.conrelid = tc.oid join pg_namespace tn on tn.oid = tc.relnamespace join pg_class fc on c.confrelid = fc.oid join pg_namespace fn on fn.oid = fc.relnamespace join lateral unnest(c.conkey) with ordinality as conseq(num, ord) on true join pg_attribute la on la.attrelid = c.conrelid and la.attnum = conseq.num join pg_attribute fa on fa.attrelid = c.confrelid and fa.attnum = c.confkey[conseq.ord] where c.contype = 'f' and tc.relname = ? and tn.nspname = coalesce(nullif(?, ''), current_schema()) group by c.conname, fn.nspname, fc.relname, c.confupdtype, c.confdeltype", []any{table, schema}, nil
		case "sqlite":
			schema = sqliteSchemaName(schema)
			return "select group_concat(\"from\") as columns, ? as foreign_schema, \"table\" as foreign_table, group_concat(\"to\") as foreign_columns, on_update, on_delete from (select * from pragma_foreign_key_list(?, ?) order by id desc, seq) group by id, \"table\", on_update, on_delete", []any{schema, table, schema}, nil
		case "sqlserver":
			return "select fk.name as name, string_agg(lc.name, ',') within group (order by fkc.constraint_column_id) as columns, fs.name as foreign_schema, ft.name as foreign_table, string_agg(fc.name, ',') within group (order by fkc.constraint_column_id) as foreign_columns, fk.update_referential_action_desc as on_update, fk.delete_referential_action_desc as on_delete from sys.foreign_keys as fk join sys.foreign_key_columns as fkc on fkc.constraint_object_id = fk.object_id join sys.tables as lt on lt.object_id = fk.parent_object_id join sys.schemas as ls on lt.schema_id = ls.schema_id join sys.columns as lc on fkc.parent_object_id = lc.object_id and fkc.parent_column_id = lc.column_id join sys.tables as ft on ft.object_id = fk.referenced_object_id join sys.schemas as fs on ft.schema_id = fs.schema_id join sys.columns as fc on fkc.referenced_object_id = fc.object_id and fkc.referenced_column_id = fc.column_id where lt.name = ? and ls.name = coalesce(nullif(?, ''), schema_name()) group by fk.name, fs.name, ft.name, fk.update_referential_action_desc, fk.delete_referential_action_desc", []any{table, schema}, nil
		}
	case "tables", "views", "types":
		var prefix, column, fallback, order string
		switch dialect {
		case "mysql":
			column, fallback = "table_schema", "table_schema not in ('information_schema', 'mysql', 'ndbinfo', 'performance_schema', 'sys')"
			order = " order by table_schema, table_name"
			switch kind {
			case "tables":
				prefix = "select table_name as `name`, table_schema as `schema`, (data_length + index_length) as `size`, table_comment as `comment`, engine as `engine`, table_collation as `collation` from information_schema.tables where table_type in ('BASE TABLE', 'SYSTEM VERSIONED') and "
			case "views":
				prefix = "select table_name as `name`, table_schema as `schema`, view_definition as `definition` from information_schema.views where "
			}
		case "postgres":
			column = "n.nspname"
			fallback = "n.nspname <> 'information_schema' and n.nspname not like 'pg\\_%'"
			order = " order by n.nspname, c.relname"
			switch kind {
			case "tables":
				prefix = "select c.relname as name, n.nspname as schema, pg_total_relation_size(c.oid) as size, obj_description(c.oid, 'pg_class') as comment from pg_class c, pg_namespace n where c.relkind in ('r', 'p') and n.oid = c.relnamespace and "
			case "views":
				column, fallback = "schemaname", "schemaname <> 'information_schema' and schemaname not like 'pg\\_%'"
				order = " order by schemaname, viewname"
				prefix = "select viewname as name, schemaname as schema, definition from pg_views where "
			case "types":
				prefix = "select t.typname as name, n.nspname as schema, t.typtype as type, t.typcategory as category, ((t.typinput = 'array_in'::regproc and t.typoutput = 'array_out'::regproc) or t.typtype = 'm') as implicit from pg_type t join pg_namespace n on n.oid = t.typnamespace left join pg_class c on c.oid = t.typrelid left join pg_type el on el.oid = t.typelem left join pg_class ce on ce.oid = el.typrelid where ((t.typrelid = 0 and (ce.relkind = 'c' or ce.relkind is null)) or c.relkind = 'c') and not exists (select 1 from pg_depend d where d.objid in (t.oid, t.typelem) and d.deptype = 'e') and "
				order = ""
			}
		case "sqlserver":
			fallback = "1 = 1"
			switch kind {
			case "tables":
				column, order = "schema_name(t.schema_id)", " group by t.name, t.schema_id order by [schema], t.name"
				prefix = "select t.name as name, schema_name(t.schema_id) as [schema], sum(u.total_pages) * 8 * 1024 as size from sys.tables as t join sys.partitions as p on p.object_id = t.object_id join sys.allocation_units as u on u.container_id = p.hobt_id where t.is_ms_shipped = 0 and t.name <> 'sysdiagrams' and "
			case "views":
				column, order = "schema_name(v.schema_id)", " order by [schema], name"
				prefix = "select name, schema_name(v.schema_id) as [schema], definition from sys.views as v inner join sys.sql_modules as m on v.object_id = m.object_id where v.is_ms_shipped = 0 and "
			}
		case "sqlite":
			if kind == "tables" {
				withSize, err := r.sqliteHasStats()
				if err != nil {
					return "", nil, err
				}
				prefix = "select tl.name as name, tl.schema as schema"
				if withSize {
					prefix += ", (select sum(s.pgsize) from (select tl.name as name union select il.name as name from pragma_index_list(tl.name, tl.schema) as il) as es join dbstat(tl.schema) as s on s.name = es.name) as size"
				}
				prefix += " from pragma_table_list as tl where "
				filter, values := schemaPredicate("tl.schema", schemas, "1 = 1")
				return prefix + filter + " and tl.type in ('table', 'virtual') and tl.name not like 'sqlite\\_%' escape '\\' order by tl.schema, tl.name", values, nil
			}
		}
		if prefix != "" {
			filter, values := schemaPredicate(column, schemas, fallback)
			return prefix + filter + order, values, nil
		}
	}
	return "", nil, errors.SchemaMetadataNotSupported.Args(kind, r.driverName)
}
