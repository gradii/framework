package schema

import (
	"context"
	"testing"

	"github.com/gradii/framework/contracts/database/db"
	"github.com/gradii/framework/contracts/database/driver"
	"github.com/gradii/framework/database/query/processors"
	"github.com/gradii/framework/errors"
	"github.com/stretchr/testify/require"
)

type scopeTestGrammar struct {
	driver.Grammar
	compiled bool
}

func (g *scopeTestGrammar) ResolveCurrentSchemas(_ driver.SchemaConnectionInfo, catalog driver.SchemaCatalog) ([]string, error) {
	rows, err := catalog.Select(driver.SchemaStatement{SQL: "scope"})
	var names []string
	for _, row := range rows {
		names = append(names, row["name"].(string))
	}
	return names, err
}
func (g *scopeTestGrammar) ResolveSchemaMetadata(query driver.SchemaMetadataQuery, catalog driver.SchemaCatalog) (driver.SchemaMetadataResult, error) {
	values := make([]any, len(query.Schemas))
	for i, value := range query.Schemas {
		values[i] = value
	}
	rows, err := catalog.Select(driver.SchemaStatement{SQL: "objects", Bindings: values})
	return driver.SchemaMetadataResult{Rows: rows}, err
}
func (g *scopeTestGrammar) CompileSchemaOperation(driver.SchemaOperation) ([]driver.SchemaStatement, error) {
	g.compiled = true
	return []driver.SchemaStatement{{SQL: "drop scoped objects"}}, nil
}
func (g *scopeTestGrammar) ForeignKeyStateQuery() string { return "" }

type scopeTestSession struct {
	db.Tx
	names    []string
	objects  []map[string]any
	failure  error
	reads    []string
	bindings []any
	writes   []string
}

func (c *scopeTestSession) SelectFromWriteConnection(dest any, sql string, bindings ...any) error {
	c.reads = append(c.reads, sql)
	if sql == "scope" {
		var rows []map[string]any
		for _, name := range c.names {
			rows = append(rows, map[string]any{"name": name})
		}
		*dest.(*[]map[string]any) = rows
		return c.failure
	}
	c.bindings = bindings
	*dest.(*[]map[string]any) = c.objects
	return nil
}
func (c *scopeTestSession) Statement(sql string, _ ...any) error {
	c.writes = append(c.writes, sql)
	return nil
}
func (*scopeTestSession) TransactionLevel() int { return 0 }

type scopeTestDB struct {
	db.DB
	session *scopeTestSession
	leases  int
}

func (d *scopeTestDB) WithWriteConnection(callback func(db.Tx) error) error {
	d.leases++
	return callback(d.session)
}

func TestDropAllScopesShareOneWriterAndRejectUnsafeMetadata(t *testing.T) {
	for _, kind := range []string{"tables", "views", "types"} {
		for _, tc := range []struct {
			name                       string
			names                      []string
			namespace                  string
			objectName                 string
			failure, wantError         error
			wantReadObjects, wantWrite bool
		}{
			{name: "in scope", names: []string{"tenant"}, namespace: "tenant", wantReadObjects: true, wantWrite: true},
			{name: "empty effective path"},
			{name: "unknown namespace", names: []string{""}, wantError: errors.SchemaCurrentNamespaceUnavailable},
			{name: "scope query failed", failure: context.Canceled, wantError: context.Canceled},
			{name: "outside object", names: []string{"tenant"}, namespace: "outside", wantReadObjects: true, wantError: errors.SchemaObjectOutsideScope},
			{name: "dotted object name", names: []string{"tenant"}, namespace: "tenant", objectName: "outside.victim", wantReadObjects: true, wantError: errors.SchemaObjectNameAmbiguous},
			{name: "dotted namespace", names: []string{"tenant.staging"}, namespace: "tenant.staging", wantReadObjects: true, wantError: errors.SchemaObjectNameAmbiguous},
		} {
			t.Run(kind+"/"+tc.name, func(t *testing.T) {
				objectName := tc.objectName
				if objectName == "" {
					objectName = "object"
				}
				connection := &scopeTestSession{names: tc.names, failure: tc.failure, objects: []map[string]any{{"name": objectName, "schema": tc.namespace, "type": "enum"}}}
				pool := &scopeTestDB{session: connection}
				grammar := &scopeTestGrammar{}
				s := &Schema{db: pool, constraints: &schemaConstraintScope{}, grammar: grammar, queryProcessor: &processors.BaseProcessor{}}
				var err error
				switch kind {
				case "tables":
					err = s.DropAllTables()
				case "views":
					err = s.DropAllViews()
				case "types":
					err = s.DropAllTypes()
				}
				if tc.wantError != nil {
					require.ErrorIs(t, err, tc.wantError)
				} else {
					require.NoError(t, err)
				}
				require.Equal(t, 1, pool.leases)
				wantReads := []string{"scope"}
				if tc.wantReadObjects {
					wantReads = append(wantReads, "objects")
					wantBindings := make([]any, len(tc.names))
					for i, name := range tc.names {
						wantBindings[i] = name
					}
					require.Equal(t, wantBindings, connection.bindings)
				}
				require.Equal(t, wantReads, connection.reads)
				require.Equal(t, tc.wantWrite, grammar.compiled)
				if tc.wantWrite {
					require.Equal(t, []string{"drop scoped objects"}, connection.writes)
				} else {
					require.Empty(t, connection.writes)
				}
			})
		}
	}
}
