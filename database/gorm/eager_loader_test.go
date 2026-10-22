package gorm

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	gormio "gorm.io/gorm"

	contractsdatabase "github.com/goravel/framework/contracts/database"
	contractsorm "github.com/goravel/framework/contracts/database/orm"
	"github.com/goravel/framework/database/orm/morphmap"
	"github.com/goravel/framework/errors"
	mocksconfig "github.com/goravel/framework/mocks/config"
)

// --- Pure helpers ----------------------------------------------------------

func TestDictKey(t *testing.T) {
	cases := []struct {
		name  string
		input any
		want  string
	}{
		{"nil", nil, ""},
		{"string", "abc", "abc"},
		{"bytes", []byte("abc"), "abc"},
		{"int", 42, "42"},
		{"int64", int64(42), "42"},
		{"uint", uint(7), "7"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, dictKey(tc.input))
		})
	}
}

func TestChunkSize(t *testing.T) {
	q := newRelQuery(t)
	// nil config -> default
	assert.Equal(t, defaultEagerLoadChunkSize, q.chunkSize())
}

// --- Reflect helpers ------------------------------------------------------

func TestCollectEagerParentsStructPtr(t *testing.T) {
	u := &relUser{ID: 1}
	out, err := collectEagerParents(u)
	assert.NoError(t, err)
	assert.Len(t, out, 1)
}

func TestCollectEagerParentsSlice(t *testing.T) {
	users := []relUser{{ID: 1}, {ID: 2}}
	out, err := collectEagerParents(&users)
	assert.NoError(t, err)
	assert.Len(t, out, 2)
}

func TestCollectEagerParentsSliceOfPtr(t *testing.T) {
	users := []*relUser{{ID: 1}, nil, {ID: 2}}
	out, err := collectEagerParents(&users)
	assert.NoError(t, err)
	assert.Len(t, out, 2)
}

func TestCollectEagerParentsNil(t *testing.T) {
	out, err := collectEagerParents(nil)
	assert.NoError(t, err)
	assert.Nil(t, out)

	var p *relUser
	out, err = collectEagerParents(p)
	assert.NoError(t, err)
	assert.Nil(t, out)
}

func TestCollectEagerParentsNotPointer(t *testing.T) {
	out, err := collectEagerParents(relUser{})
	assert.NoError(t, err)
	assert.Nil(t, out)
}

func TestCollectEagerParentsUnsupportedKind(t *testing.T) {
	v := 7
	out, err := collectEagerParents(&v)
	assert.NoError(t, err)
	assert.Nil(t, out)
}

func TestNewSampleModel(t *testing.T) {
	u := relUser{ID: 1}
	rv := reflect.ValueOf(u)
	got := newSampleModel(rv)
	rt := reflect.TypeOf(got)
	assert.Equal(t, reflect.Pointer, rt.Kind())
	assert.Equal(t, "relUser", rt.Elem().Name())
	// Should be a fresh zero instance (not the original).
	assert.Equal(t, uint(0), got.(*relUser).ID)
}

func TestParseSchema(t *testing.T) {
	db := newStubGormDB(t)
	s, err := parseSchema(db, &relUser{})
	assert.NoError(t, err)
	assert.Equal(t, "rel_users", s.Table)

	_, err = parseSchema(db, "bad-model")
	assert.Error(t, err)
}

func TestExtractKeysDeduplicatesAndSkipsZero(t *testing.T) {
	db := newStubGormDB(t)
	s, err := parseSchema(db, &relUser{})
	assert.NoError(t, err)
	idField := s.FieldsByDBName["id"]
	assert.NotNil(t, idField)

	q := NewQuery(context.Background(), nil, contractsdatabase.Config{}, db, nil, nil, nil, &Conditions{}, nil)

	parents := []reflect.Value{
		reflect.ValueOf(relUser{ID: 1}),
		reflect.ValueOf(relUser{ID: 2}),
		reflect.ValueOf(relUser{ID: 1}), // dup
		reflect.ValueOf(relUser{ID: 0}), // zero - skipped
	}
	keys := extractKeys(q, parents, idField)
	assert.Len(t, keys, 2)
}

// --- setRelationField ------------------------------------------------------

type withPtrRel struct {
	ID      uint
	Profile *relProfile
}

type withSlicePtrRel struct {
	ID    uint
	Books []*relBook
}

type withSliceStructRel struct {
	ID    uint
	Books []relBook
}

func TestSetRelationField_PtrAssignment(t *testing.T) {
	parent := withPtrRel{}
	rv := reflect.ValueOf(&parent).Elem()
	row := reflect.ValueOf(&relProfile{Bio: "x"})
	err := setRelationField(rv, "Profile", []reflect.Value{row})
	assert.NoError(t, err)
	assert.Equal(t, "x", parent.Profile.Bio)
}

func TestSetRelationField_PtrEmptyClearsField(t *testing.T) {
	parent := withPtrRel{Profile: &relProfile{Bio: "stale"}}
	rv := reflect.ValueOf(&parent).Elem()
	err := setRelationField(rv, "Profile", nil)
	assert.NoError(t, err)
	assert.Nil(t, parent.Profile)
}

func TestSetRelationField_SliceOfPtrs(t *testing.T) {
	parent := withSlicePtrRel{}
	rv := reflect.ValueOf(&parent).Elem()
	rows := []reflect.Value{
		reflect.ValueOf(&relBook{Title: "a"}),
		reflect.ValueOf(&relBook{Title: "b"}),
	}
	err := setRelationField(rv, "Books", rows)
	assert.NoError(t, err)
	assert.Len(t, parent.Books, 2)
}

func TestSetRelationField_SliceOfStructs(t *testing.T) {
	parent := withSliceStructRel{}
	rv := reflect.ValueOf(&parent).Elem()
	rows := []reflect.Value{
		reflect.ValueOf(&relBook{Title: "a"}),
		reflect.ValueOf(&relBook{Title: "b"}),
	}
	err := setRelationField(rv, "Books", rows)
	assert.NoError(t, err)
	assert.Len(t, parent.Books, 2)
	assert.Equal(t, "a", parent.Books[0].Title)
}

func TestSetRelationField_UnknownField(t *testing.T) {
	parent := withPtrRel{}
	rv := reflect.ValueOf(&parent).Elem()
	err := setRelationField(rv, "Missing", nil)
	assert.True(t, errors.Is(err, errors.OrmEagerLoadCannotAssign))
}

// withInterfaceRel exercises the MorphTo field shape: an `any` field that the loader fills with
// a *RelatedModel value chosen at runtime via the morph map.
type withInterfaceRel struct {
	ID        uint
	Imageable any
}

func TestSetRelationField_InterfaceAssignment(t *testing.T) {
	parent := withInterfaceRel{}
	rv := reflect.ValueOf(&parent).Elem()
	row := reflect.ValueOf(&relBook{Title: "x"})
	err := setRelationField(rv, "Imageable", []reflect.Value{row})
	assert.NoError(t, err)
	got, ok := parent.Imageable.(*relBook)
	assert.True(t, ok)
	assert.Equal(t, "x", got.Title)
}

func TestSetRelationField_InterfaceEmptyClearsField(t *testing.T) {
	parent := withInterfaceRel{Imageable: &relBook{Title: "stale"}}
	rv := reflect.ValueOf(&parent).Elem()
	err := setRelationField(rv, "Imageable", nil)
	assert.NoError(t, err)
	assert.Nil(t, parent.Imageable)
}

// --- runEagerLoads no-op paths --------------------------------------------

func TestRunEagerLoadsNoParents(t *testing.T) {
	q := newRelQuery(t)
	err := q.runEagerLoads(nil, []eagerLoadEntry{{relation: "Books"}})
	assert.NoError(t, err)
}

func TestRunEagerLoadsNoEntries(t *testing.T) {
	q := newRelQuery(t)
	parents := []reflect.Value{reflect.ValueOf(relUser{ID: 1})}
	err := q.runEagerLoads(parents, nil)
	assert.NoError(t, err)
}

func TestApplyEagerLoadsNothingQueued(t *testing.T) {
	q := newRelQuery(t)
	users := &[]relUser{}
	err := q.applyEagerLoads(users)
	assert.NoError(t, err)
}

func TestApplyEagerLoadsEmptyDest(t *testing.T) {
	q := newRelQuery(t)
	q.conditions.eagerLoad = []eagerLoadEntry{{relation: "Books"}}
	users := &[]relUser{}
	err := q.applyEagerLoads(users)
	assert.NoError(t, err)
}

func TestRecurseNestedNoop(t *testing.T) {
	q := newRelQuery(t)
	err := q.recurseNested(nil, []eagerLoadEntry{{relation: "X"}})
	assert.NoError(t, err)
	err = q.recurseNested([]reflect.Value{reflect.ValueOf(relUser{})}, nil)
	assert.NoError(t, err)
}

func TestMaybeRecurseEmpty_NotMany(t *testing.T) {
	q := newRelQuery(t)
	err := q.maybeRecurseEmpty(nil, "X", false, nil)
	assert.NoError(t, err)
}

func TestMaybeRecurseEmpty_ManyAssignsEmptySlices(t *testing.T) {
	q := newRelQuery(t)
	u1 := &withSlicePtrRel{ID: 1, Books: []*relBook{{Title: "a"}}}
	u2 := &withSlicePtrRel{ID: 2}
	parents := []reflect.Value{reflect.ValueOf(u1).Elem(), reflect.ValueOf(u2).Elem()}
	err := q.maybeRecurseEmpty(parents, "Books", true, nil)
	assert.NoError(t, err)
	assert.Empty(t, u1.Books)
	assert.Empty(t, u2.Books)
}

// --- Phase D: Pivot column hydration tests --------------------------------

// roleUserPivot is a sample custom Pivot model used by the struct-only Pivot tests below. The
// gorm tags lock column names so the test data (keyed by db column) maps deterministically into
// struct fields.
type roleUserPivot struct {
	UserID   uint   `gorm:"column:user_id"`
	RoleID   uint   `gorm:"column:role_id"`
	Priority string `gorm:"column:priority"`
	Notes    string `gorm:"column:notes"`
}

func (roleUserPivot) TableName() string { return "role_user_pivots" }

type roleWithPivot struct {
	ID    uint
	Name  string
	Pivot roleUserPivot `gorm:"-"`
}

func (roleWithPivot) TableName() string { return "role_with_pivots" }

type roleWithoutPivot struct {
	ID   uint
	Name string
}

func (roleWithoutPivot) TableName() string { return "role_without_pivots" }

// roleWithCustomPivotField has the pivot data on a non-default field name — exercises
// PivotField configuration.
type roleWithCustomPivotField struct {
	ID        uint
	Name      string
	UserPivot roleUserPivot `gorm:"-"`
}

func (roleWithCustomPivotField) TableName() string { return "role_with_custom_pivot_fields" }

// roleWithBadPivot has a Pivot field that isn't a struct — exercises the field-not-struct guard.
type roleWithBadPivot struct {
	ID    uint
	Name  string
	Pivot string `gorm:"-"`
}

func (roleWithBadPivot) TableName() string { return "role_with_bad_pivots" }

func TestWritePivotField_HydratesStructField(t *testing.T) {
	role := &roleWithPivot{ID: 1, Name: "admin"}
	plan := mustPivotPlan(t, "Pivot", &roleUserPivot{})
	err := writePivotField(t.Context(), reflect.ValueOf(role), map[string]any{
		"priority": "high",
		"notes":    "test",
	}, plan)
	assert.NoError(t, err)
	assert.Equal(t, "high", role.Pivot.Priority)
	assert.Equal(t, "test", role.Pivot.Notes)
}

func TestWritePivotField_TypedColumns(t *testing.T) {
	role := &roleWithPivot{ID: 1}
	plan := mustPivotPlan(t, "Pivot", &roleUserPivot{})
	err := writePivotField(t.Context(), reflect.ValueOf(role), map[string]any{
		"user_id": uint(7),
		"role_id": uint(99),
	}, plan)
	assert.NoError(t, err)
	assert.Equal(t, uint(7), role.Pivot.UserID)
	assert.Equal(t, uint(99), role.Pivot.RoleID)
}

func TestWritePivotField_CustomFieldName(t *testing.T) {
	role := &roleWithCustomPivotField{ID: 1}
	plan := mustPivotPlan(t, "UserPivot", &roleUserPivot{})
	err := writePivotField(t.Context(), reflect.ValueOf(role), map[string]any{
		"user_id":  uint(7),
		"priority": "high",
	}, plan)
	assert.NoError(t, err)
	assert.Equal(t, uint(7), role.UserPivot.UserID)
	assert.Equal(t, "high", role.UserPivot.Priority)
}

func TestWritePivotField_NoPivotField_ReturnsNil(t *testing.T) {
	role := &roleWithoutPivot{ID: 1, Name: "admin"}
	plan := mustPivotPlan(t, "Pivot", &roleUserPivot{})
	err := writePivotField(t.Context(), reflect.ValueOf(role), map[string]any{"priority": "high"}, plan)
	assert.NoError(t, err, "writePivotField must silently skip when configured field is absent")
}

func TestWritePivotField_UnknownColumn_Skipped(t *testing.T) {
	role := &roleWithPivot{ID: 1}
	plan := mustPivotPlan(t, "Pivot", &roleUserPivot{})
	err := writePivotField(t.Context(), reflect.ValueOf(role), map[string]any{
		"priority":   "high",
		"unknown_xy": "ignored",
	}, plan)
	assert.NoError(t, err)
	assert.Equal(t, "high", role.Pivot.Priority)
}

func TestPreparePivotHydration_NoFieldOnRelated_ReturnsNil(t *testing.T) {
	q := newRelQueryWith(t, &relUser{})
	desc := &relationDescriptor{
		relatedModel: &roleWithoutPivot{},
		pivotField:   "Pivot",
	}
	plan, err := preparePivotHydration(q, desc)
	assert.NoError(t, err)
	assert.Nil(t, plan, "no field by configured name means nothing to hydrate")
}

func TestPreparePivotHydration_FieldNotStruct_Errors(t *testing.T) {
	q := newRelQueryWith(t, &relUser{})
	desc := &relationDescriptor{
		relatedModel: &roleWithBadPivot{},
		pivotField:   "Pivot",
	}
	_, err := preparePivotHydration(q, desc)
	assert.True(t, errors.Is(err, errors.OrmRelationPivotFieldNotStruct))
}

func TestPreparePivotHydration_DefaultPivot_FromFieldType(t *testing.T) {
	q := newRelQueryWith(t, &relUser{})
	desc := &relationDescriptor{
		relatedModel: &roleWithPivot{},
		pivotField:   "Pivot",
	}
	plan, err := preparePivotHydration(q, desc)
	assert.NoError(t, err)
	assert.NotNil(t, plan)
	assert.Equal(t, "Pivot", plan.fieldName)
	// Selected columns include every db-tagged field on roleUserPivot.
	assert.ElementsMatch(t, []string{"user_id", "role_id", "priority", "notes"}, plan.extraColumns)
}

func TestPreparePivotHydration_CustomFieldName_FromFieldType(t *testing.T) {
	q := newRelQueryWith(t, &relUser{})
	desc := &relationDescriptor{
		relatedModel: &roleWithCustomPivotField{},
		pivotField:   "UserPivot",
	}
	plan, err := preparePivotHydration(q, desc)
	assert.NoError(t, err)
	assert.NotNil(t, plan)
	assert.Equal(t, "UserPivot", plan.fieldName)
}

// mustPivotPlan builds a pivotHydrationPlan for use in writePivotField tests, bypassing
// preparePivotHydration so we can exercise writePivotField independently.
func mustPivotPlan(t *testing.T, fieldName string, pivotProto any) *pivotHydrationPlan {
	t.Helper()
	q := newRelQueryWith(t, &relUser{})
	usingSchema, err := parseSchema(q.instance, pivotProto)
	if err != nil {
		t.Fatalf("parse pivot schema: %v", err)
	}
	cols := make([]string, 0, len(usingSchema.Fields))
	byCol := make(map[string]*schemaField, len(usingSchema.Fields))
	for _, f := range usingSchema.Fields {
		if f.DBName == "" {
			continue
		}
		cols = append(cols, f.DBName)
		byCol[f.DBName] = f
	}
	return &pivotHydrationPlan{
		fieldName:     fieldName,
		extraColumns:  cols,
		fieldByColumn: byCol,
	}
}

// Additional edge case coverage

func TestDictKey_AdditionalTypes(t *testing.T) {
	assert.Equal(t, "123", dictKey(uint64(123)))
	assert.Equal(t, "123.45", dictKey(float64(123.45)))
	assert.Equal(t, "true", dictKey(true))
	assert.Equal(t, "false", dictKey(false))
}

func TestCollectEagerParents_EdgeCases(t *testing.T) {
	// Empty slice
	users := []relUser{}
	out, err := collectEagerParents(&users)
	assert.NoError(t, err)
	assert.Empty(t, out)

	// Slice with all nils
	nilUsers := []*relUser{nil, nil, nil}
	out, err = collectEagerParents(&nilUsers)
	assert.NoError(t, err)
	assert.Empty(t, out)
}

func TestSetRelationField_EdgeCases(t *testing.T) {
	// Clearing slice field
	parent := withSlicePtrRel{Books: []*relBook{{Title: "old"}}}
	rv := reflect.ValueOf(&parent).Elem()
	err := setRelationField(rv, "Books", nil)
	assert.NoError(t, err)
	assert.Empty(t, parent.Books)

	// Empty slice of structs
	parent2 := withSliceStructRel{Books: []relBook{{Title: "old"}}}
	rv2 := reflect.ValueOf(&parent2).Elem()
	err = setRelationField(rv2, "Books", []reflect.Value{})
	assert.NoError(t, err)
	assert.Empty(t, parent2.Books)
}

func TestWritePivotField_EdgeCases(t *testing.T) {
	// Empty data
	role := &roleWithPivot{ID: 1, Name: "admin"}
	plan := mustPivotPlan(t, "Pivot", &roleUserPivot{})
	err := writePivotField(t.Context(), reflect.ValueOf(role), map[string]any{}, plan)
	assert.NoError(t, err)
	assert.Equal(t, "", role.Pivot.Priority)

	// Nil data
	role2 := &roleWithPivot{ID: 2, Name: "user"}
	err = writePivotField(t.Context(), reflect.ValueOf(role2), nil, plan)
	assert.NoError(t, err)
}

func TestExtractKeys_EmptyInput(t *testing.T) {
	db := newStubGormDB(t)
	s, err := parseSchema(db, &relUser{})
	assert.NoError(t, err)
	idField := s.FieldsByDBName["id"]

	q := NewQuery(context.Background(), nil, contractsdatabase.Config{}, db, nil, nil, nil, &Conditions{}, nil)
	keys := extractKeys(q, []reflect.Value{}, idField)
	assert.Empty(t, keys)
}

func TestNewSampleModel_WithPointer(t *testing.T) {
	u := &relUser{ID: 1}
	rv := reflect.ValueOf(u)
	got := newSampleModel(rv)
	rt := reflect.TypeOf(got)
	assert.Equal(t, reflect.Pointer, rt.Kind())
	// When input is a pointer, output is pointer-to-pointer
	assert.Equal(t, reflect.Pointer, rt.Elem().Kind())
}

func TestApplyEagerLoads_WithNilDest(t *testing.T) {
	q := newRelQuery(t)
	q.conditions.eagerLoad = []eagerLoadEntry{{relation: "Books"}}
	err := q.applyEagerLoads(nil)
	assert.NoError(t, err)
}

func TestRunEagerLoads_InvalidRelation(t *testing.T) {
	q := newRelQuery(t)
	parents := []reflect.Value{reflect.ValueOf(relUser{ID: 1})}
	err := q.runEagerLoads(parents, []eagerLoadEntry{{relation: "NonExistent"}})
	assert.Error(t, err)
}

// --- Loader-driving harness ------------------------------------------------
//
// The loader tests below pin the statement a loader builds and executes, so they must observe the
// loader's own call site rather than a child the test rebuilt by hand. A loader compiles its child
// and runs it through the connection pool (executor.go), which the stub gorm.DB used elsewhere in
// the package cannot provide: it has no *sql.DB behind it. These helpers give the Query a real
// *sql.DB backed by a recording driver, in the connector-with-a-stub-driver shape database/driver's
// tests use to hand a gorm.DB a usable pool. Every statement the loader executes is captured and an
// empty result set comes back, so the loader completes its read path without a database and the
// assertion lands on the SQL the loader itself produced.

type recordedStatement struct {
	sql  string
	args []driver.Value
}

// cannedDriverResult is a result set the recording driver hands back for any statement whose SQL
// contains match; every other statement gets the empty result. It lets a loader with more than one
// read — a pivot or through read followed by a related read — be driven past its first read, so the
// statement the loader executes for each can be pinned. Without it the loader would take its
// empty-read early return and never reach the second read.
type cannedDriverResult struct {
	match   string
	columns []string
	rows    [][]driver.Value
}

type recordingSQLDriver struct {
	mu         sync.Mutex
	statements []recordedStatement
	canned     []cannedDriverResult
}

func (d *recordingSQLDriver) Open(string) (driver.Conn, error) { return &recordingConn{driver: d}, nil }

func (d *recordingSQLDriver) record(sql string, args []driver.Value) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.statements = append(d.statements, recordedStatement{sql: sql, args: args})
}

func (d *recordingSQLDriver) last(t *testing.T) recordedStatement {
	t.Helper()

	d.mu.Lock()
	defer d.mu.Unlock()

	if len(d.statements) == 0 {
		t.Fatalf("the loader executed no statement")
	}

	return d.statements[len(d.statements)-1]
}

// returnRows makes the driver hand back rows for any later statement whose SQL contains match. The
// result set is replayed once; a match carrying no rows is still a result set, distinct from the
// empty default.
func (d *recordingSQLDriver) returnRows(match string, columns []string, rows ...[]driver.Value) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.canned = append(d.canned, cannedDriverResult{match: match, columns: columns, rows: rows})
}

// cannedFor returns a fresh result set for sql when a canned result matches it, else nil.
func (d *recordingSQLDriver) cannedFor(sql string) driver.Rows {
	d.mu.Lock()
	defer d.mu.Unlock()

	for _, c := range d.canned {
		if strings.Contains(sql, c.match) {
			return &cannedDriverRows{columns: c.columns, rows: c.rows}
		}
	}

	return nil
}

// count reports how many statements the driver has seen, so a test can pin that a loader ran each of
// its reads exactly once.
func (d *recordingSQLDriver) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()

	return len(d.statements)
}

// at returns the statement at index i, failing the test when the loader executed fewer.
func (d *recordingSQLDriver) at(t *testing.T, i int) recordedStatement {
	t.Helper()

	d.mu.Lock()
	defer d.mu.Unlock()

	if i >= len(d.statements) {
		t.Fatalf("the loader executed %d statement(s); want at least %d", len(d.statements), i+1)
	}

	return d.statements[i]
}

type recordingConn struct{ driver *recordingSQLDriver }

func (c *recordingConn) Prepare(query string) (driver.Stmt, error) {
	return &recordingStmt{driver: c.driver, query: query}, nil
}
func (c *recordingConn) Close() error              { return nil }
func (c *recordingConn) Begin() (driver.Tx, error) { return nil, driver.ErrSkip }

type recordingStmt struct {
	driver *recordingSQLDriver
	query  string
}

func (s *recordingStmt) Close() error  { return nil }
func (s *recordingStmt) NumInput() int { return -1 }
func (s *recordingStmt) Exec([]driver.Value) (driver.Result, error) {
	return driver.RowsAffected(0), nil
}
func (s *recordingStmt) Query(args []driver.Value) (driver.Rows, error) {
	s.driver.record(s.query, args)

	if rows := s.driver.cannedFor(s.query); rows != nil {
		return rows, nil
	}

	return emptyDriverRows{}, nil
}

type emptyDriverRows struct{}

func (emptyDriverRows) Columns() []string              { return nil }
func (emptyDriverRows) Close() error                   { return nil }
func (emptyDriverRows) Next(dest []driver.Value) error { return io.EOF }

// cannedDriverRows replays a canned result set once and reports EOF thereafter. It is how a loader
// with two reads is driven past its first: the pivot or through read returns a row, so the loader
// goes on to build and run the related read the test wants to pin.
type cannedDriverRows struct {
	columns []string
	rows    [][]driver.Value
	index   int
}

func (r *cannedDriverRows) Columns() []string { return r.columns }
func (r *cannedDriverRows) Close() error      { return nil }
func (r *cannedDriverRows) Next(dest []driver.Value) error {
	if r.index >= len(r.rows) {
		return io.EOF
	}

	copy(dest, r.rows[r.index])
	r.index++

	return nil
}

type recordingConnector struct{ driver *recordingSQLDriver }

func (c recordingConnector) Connect(context.Context) (driver.Conn, error) {
	return &recordingConn{driver: c.driver}, nil
}
func (c recordingConnector) Driver() driver.Driver { return c.driver }

// newRecordingRelQuery builds a Query shaped like newRelQueryWith, but whose connection pool records
// the statements the loaders execute.
func newRecordingRelQuery(t *testing.T, model any) (*Query, *recordingSQLDriver) {
	t.Helper()

	recordDriver := &recordingSQLDriver{}
	sqlDB := sql.OpenDB(recordingConnector{driver: recordDriver})
	t.Cleanup(func() { _ = sqlDB.Close() })

	db, err := gormio.Open(stubDialector{}, &gormio.Config{
		ConnPool:             sqlDB,
		DisableAutomaticPing: true,
	})
	if err != nil {
		t.Fatalf("open recording gorm: %v", err)
	}

	conditions := Conditions{model: model}
	return NewQuery(context.Background(), nil, contractsdatabase.Config{}, db, nil, nil, nil, &conditions, nil), recordDriver
}

// driverBindings maps a chunk of keys to the driver.Value slice database/sql hands the driver: an
// unsigned key becomes an int64 through the default parameter converter, and a string stays a
// string. It lets the loader-driven assertions pin the bindings exactly.
func driverBindings(keys []any) []driver.Value {
	out := make([]driver.Value, len(keys))
	for i, key := range keys {
		switch value := key.(type) {
		case uint:
			out[i] = int64(value)
		case int:
			// database/sql's default parameter converter widens an int to int64 before the driver
			// sees it, so a scope binding written as 1 arrives as int64(1).
			out[i] = int64(value)
		default:
			out[i] = key
		}
	}

	return out
}

// --- loadBelongsTo ---------------------------------------------------------

// TestLoadBelongsTo_ChildQueryIsCompiled drives loadBelongsTo itself: it calls the loader with
// parent rows carrying the owner key and asserts the statement the loader built and executed — the
// related table filtered by its owner key, addressed through relatedChild so the framework compiler
// renders the statement instead of GORM.
//
// Each case also renders the GORM statement the loader used to hand over and compares the two after
// normalisation, so the conversion cannot drift from the SQL it replaced. The bindings the driver
// received are asserted exactly: same key type, same order, same arity.
func TestLoadBelongsTo_ChildQueryIsCompiled(t *testing.T) {
	// Case and keyword spacing differ between the two builders; the predicate does not.
	normalize := func(sql string) string {
		sql = strings.Join(strings.Fields(strings.ToLower(sql)), " ")
		return strings.ReplaceAll(sql, ", ", ",")
	}

	cases := []struct {
		name  string
		chunk []any
		sql   string
	}{
		{
			name:  "two keys",
			chunk: []any{uint(5), uint(6)},
			sql:   `select * from "rel_users" where "rel_users"."id" in (?, ?)`,
		},
		{
			name:  "one key",
			chunk: []any{uint(5)},
			sql:   `select * from "rel_users" where "rel_users"."id" in (?)`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q, recorder := newRecordingRelQuery(t, &relBook{})
			desc, err := resolveRelation(q.instance, &relBook{}, "Author")
			assert.NoError(t, err)
			if err != nil {
				return
			}

			assert.Equal(t, relKindBelongsTo, desc.kind)
			assert.Equal(t, "rel_users", desc.relatedTable)

			ref := desc.references[0]

			// The parents carry the owner key the loader reads for a BelongsTo: the related key sits
			// on the parent's foreign key.
			parents := make([]reflect.Value, 0, len(c.chunk))
			for _, key := range c.chunk {
				parent := &relBook{AuthorID: key.(uint)}
				parents = append(parents, reflect.ValueOf(parent).Elem())
			}

			// Drive the loader itself, so the assertion is on the statement the loader built and ran
			// rather than on a child this test rebuilt by hand.
			assert.NoError(t, q.loadBelongsTo(parents, &relBook{}, desc, eagerLoadEntry{relation: "Author"}, nil))

			recorded := recorder.last(t)
			assert.Equal(t, c.sql, recorded.sql)
			assert.Equal(t, driverBindings(c.chunk), recorded.args)

			// Keep the legacy-versus-compiled comparison: the statement the loader executed must
			// render the same predicate the GORM chain it replaced produced.
			legacy := q.freshSession().Table(desc.relatedTable).
				Where(fmt.Sprintf("%s IN ?", q.ident(desc.relatedTable, ref.primaryColumn)), c.chunk)
			statement := legacy.Session(&gormio.Session{DryRun: true}).Find(&[]*relUser{})
			assert.NoError(t, statement.Error)
			assert.Equal(t, normalize(statement.Statement.SQL.String()), normalize(recorded.sql),
				"the loader's compiled child must render the predicate the GORM statement did")
		})
	}

	// The owner key survives column pruning: the loader passes it as a required column, so the child
	// still selects it and the dictionary can be keyed off the related rows.
	t.Run("pruned select keeps the owner key", func(t *testing.T) {
		q := newRelQueryWith(t, &relBook{})
		desc, err := resolveRelation(q.instance, &relBook{}, "Author")
		assert.NoError(t, err)
		if err != nil {
			return
		}
		ref := desc.references[0]

		child := q.relatedChild(desc.relatedTable, desc.relatedTable+"."+ref.primaryColumn, []any{uint(5)})
		conditions := child.conditions
		conditions.selectItems = []any{"name", ref.primaryColumn}

		compiled, bindings, err := child.setConditions(conditions).compileSelect()
		assert.NoError(t, err)
		assert.Equal(t, `select "name", "id" from "rel_users" where "rel_users"."id" in (?)`, compiled)
		assert.Equal(t, []any{uint(5)}, bindings)
	})

	// BelongsTo starts from the related table alone, with no model of its own, so the child inherits
	// none of the parent query's state — the same shape the fresh GORM session had. A related model's
	// soft-delete scope and global scopes therefore stay off the eager load, which is also why the
	// compiled child carries no extra predicate.
	t.Run("child carries no inherited conditions", func(t *testing.T) {
		q := newRelQueryWith(t, &relBook{})
		desc, err := resolveRelation(q.instance, &relBook{}, "Author")
		assert.NoError(t, err)
		if err != nil {
			return
		}
		ref := desc.references[0]

		child := q.relatedChild(desc.relatedTable, desc.relatedTable+"."+ref.primaryColumn, []any{uint(5)})
		assert.Nil(t, child.conditions.model)
		assert.Nil(t, child.conditions.dest)
		assert.False(t, child.conditions.withTrashed)
		assert.Empty(t, child.conditions.withoutGlobalScopes)
		assert.Empty(t, child.conditions.scopes)
		assert.Len(t, child.conditions.where, 1)
	})
}

// --- loadHasOneOrMany ------------------------------------------------------

// TestLoadHasOneOrMany_ChildQueryIsCompiled drives loadHasOneOrMany itself for both kinds it serves
// — a HasMany and a HasOne — with parent rows carrying the owner key, and asserts the statement the
// loader built and executed: the related table filtered by the foreign key, addressed through
// relatedChild so the framework compiler renders the statement instead of GORM.
//
// The loader is shared by the two kinds; only the way it attaches the rows differs, so the same
// child statement has to come out of both. Each case also renders the GORM statement the loader used
// to hand over and compares the two after normalisation, so the conversion cannot drift from the SQL
// it replaced. The bindings the driver received are asserted exactly: same key type, same order,
// same arity.
func TestLoadHasOneOrMany_ChildQueryIsCompiled(t *testing.T) {
	// Case and keyword spacing differ between the two builders; the predicate does not.
	normalize := func(sql string) string {
		sql = strings.Join(strings.Fields(strings.ToLower(sql)), " ")
		return strings.ReplaceAll(sql, ", ", ",")
	}

	cases := []struct {
		name     string
		parent   any
		relation string
		kind     relationKind
		table    string
		isMany   bool
		dest     any
		chunk    []any
		sql      string
	}{
		{
			name:     "has many",
			parent:   &relUser{},
			relation: "Books",
			kind:     relKindHasMany,
			table:    "rel_books",
			isMany:   true,
			dest:     &[]*relBook{},
			chunk:    []any{uint(3), uint(4)},
			sql:      `select * from "rel_books" where "rel_books"."user_id" in (?, ?)`,
		},
		{
			name:     "has one",
			parent:   &relUser{},
			relation: "Profile",
			kind:     relKindHasOne,
			table:    "rel_profiles",
			isMany:   false,
			dest:     &relProfile{},
			chunk:    []any{uint(7)},
			sql:      `select * from "rel_profiles" where "rel_profiles"."user_id" in (?)`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q, recorder := newRecordingRelQuery(t, c.parent)
			desc, err := resolveRelation(q.instance, c.parent, c.relation)
			assert.NoError(t, err)
			if err != nil {
				return
			}

			assert.Equal(t, c.kind, desc.kind)
			assert.Equal(t, c.table, desc.relatedTable)

			ref := desc.references[0]

			// The parents carry the owner key the loader reads for a HasOne/HasMany: the parent's own
			// primary key, which the child's foreign key matches.
			parents := make([]reflect.Value, 0, len(c.chunk))
			for _, key := range c.chunk {
				parent := &relUser{ID: key.(uint)}
				parents = append(parents, reflect.ValueOf(parent).Elem())
			}

			// Drive the loader itself, so the assertion is on the statement the loader built and ran
			// rather than on a child this test rebuilt by hand.
			assert.NoError(t, q.loadHasOneOrMany(parents, c.parent, desc, eagerLoadEntry{relation: c.relation}, nil, c.isMany))

			recorded := recorder.last(t)
			assert.Equal(t, c.sql, recorded.sql)
			assert.Equal(t, driverBindings(c.chunk), recorded.args)

			// Keep the legacy-versus-compiled comparison: the statement the loader executed must
			// render the same predicate the GORM chain it replaced produced.
			legacy := q.freshSession().Table(desc.relatedTable).
				Where(fmt.Sprintf("%s IN ?", q.ident(desc.relatedTable, ref.foreignColumn)), c.chunk)
			statement := legacy.Session(&gormio.Session{DryRun: true}).Find(c.dest)
			assert.NoError(t, statement.Error)
			assert.Equal(t, normalize(statement.Statement.SQL.String()), normalize(recorded.sql),
				"the loader's compiled child must render the predicate the GORM statement did")
		})
	}

	// The loader passes the foreign key as a required column, so it survives column pruning: the
	// child still selects it and the dictionary can be keyed off the related rows.
	t.Run("pruned select keeps the foreign key", func(t *testing.T) {
		q := newRelQueryWith(t, &relUser{})
		desc, err := resolveRelation(q.instance, &relUser{}, "Books")
		assert.NoError(t, err)
		if err != nil {
			return
		}
		ref := desc.references[0]

		child := q.relatedChild(desc.relatedTable, desc.relatedTable+"."+ref.foreignColumn, []any{uint(3)})
		conditions := child.conditions
		conditions.selectItems = []any{"title", ref.foreignColumn}

		compiled, bindings, err := child.setConditions(conditions).compileSelect()
		assert.NoError(t, err)
		assert.Equal(t, `select "title", "user_id" from "rel_books" where "rel_books"."user_id" in (?)`, compiled)
		assert.Equal(t, []any{uint(3)}, bindings)
	})

	// The child addresses the related table alone, with no model of its own, so it inherits none of
	// the parent query's state — the same shape the fresh GORM session had. A related model's
	// soft-delete scope and global scopes therefore stay off the eager load, which is also why the
	// compiled child carries no extra predicate.
	t.Run("child carries no inherited conditions", func(t *testing.T) {
		q := newRelQueryWith(t, &relUser{})
		desc, err := resolveRelation(q.instance, &relUser{}, "Books")
		assert.NoError(t, err)
		if err != nil {
			return
		}
		ref := desc.references[0]

		child := q.relatedChild(desc.relatedTable, desc.relatedTable+"."+ref.foreignColumn, []any{uint(3)})
		assert.Nil(t, child.conditions.model)
		assert.Nil(t, child.conditions.dest)
		assert.False(t, child.conditions.withTrashed)
		assert.Empty(t, child.conditions.withoutGlobalScopes)
		assert.Empty(t, child.conditions.scopes)
		assert.Len(t, child.conditions.where, 1)
	})
}

// --- loadMorph -------------------------------------------------------------

// TestLoadMorph_ChildQueryIsCompiled drives loadMorph itself for a polymorphic one-or-many
// relation: it calls the loader with parent rows carrying the owner key and asserts the statement
// the loader built and executed — the related table filtered by the foreign key and by the
// relation's own morph value, addressed through relatedChild so the framework compiler renders the
// statement instead of GORM.
//
// Each case also renders the GORM statement the loader used to hand over and compares the two after
// normalisation, so the conversion cannot drift from the SQL it replaced. The bindings the driver
// received are asserted exactly: the chunk's keys first, then the morph value.
func TestLoadMorph_ChildQueryIsCompiled(t *testing.T) {
	// Case and keyword spacing differ between the two builders; the predicate does not.
	normalize := func(sql string) string {
		sql = strings.Join(strings.Fields(strings.ToLower(sql)), " ")
		return strings.ReplaceAll(sql, ", ", ",")
	}

	cases := []struct {
		name     string
		parent   any
		relation string
		kind     relationKind
		table    string
		typeCol  string
		dest     any
		chunk    []any
		sql      string
	}{
		{
			name:     "morph one",
			parent:   &relUser{},
			relation: "Logo",
			kind:     relKindMorphOne,
			table:    "rel_logos",
			typeCol:  "logoable_type",
			dest:     &relLogo{},
			chunk:    []any{uint(9), uint(10)},
			sql:      `select * from "rel_logos" where "rel_logos"."logoable_id" in (?, ?) and "rel_logos"."logoable_type" = ?`,
		},
		{
			name:     "morph many",
			parent:   &relUser{},
			relation: "Houses",
			kind:     relKindMorphMany,
			table:    "rel_houses",
			typeCol:  "houseable_type",
			dest:     &[]relHouse{},
			chunk:    []any{uint(9)},
			sql:      `select * from "rel_houses" where "rel_houses"."houseable_id" in (?) and "rel_houses"."houseable_type" = ?`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q, recorder := newRecordingRelQuery(t, c.parent)
			desc, err := resolveRelation(q.instance, c.parent, c.relation)
			assert.NoError(t, err)
			if err != nil {
				return
			}

			assert.Equal(t, c.kind, desc.kind)
			assert.Equal(t, c.table, desc.relatedTable)
			assert.Equal(t, c.typeCol, desc.morphTypeColumn)

			ref := desc.references[0]

			// The parents carry the owner key the loader reads for a morph one / many: the parent's
			// primary key.
			parents := make([]reflect.Value, 0, len(c.chunk))
			for _, key := range c.chunk {
				parent := &relUser{ID: key.(uint)}
				parents = append(parents, reflect.ValueOf(parent).Elem())
			}

			// Drive the loader itself, so the assertion is on the statement the loader built and ran
			// rather than on a child this test rebuilt by hand.
			assert.NoError(t, q.loadMorph(parents, c.parent, desc, eagerLoadEntry{relation: c.relation}, nil, c.kind == relKindMorphMany))

			recorded := recorder.last(t)
			assert.Equal(t, c.sql, recorded.sql)
			assert.Equal(t, append(driverBindings(c.chunk), desc.morphValue), recorded.args)

			// Keep the legacy-versus-compiled comparison: the statement the loader executed must
			// render the same predicates the GORM chain it replaced produced.
			legacy := q.freshSession().Table(desc.relatedTable).
				Where(fmt.Sprintf("%s IN ?", q.ident(desc.relatedTable, ref.foreignColumn)), c.chunk).
				Where(fmt.Sprintf("%s = ?", q.ident(desc.relatedTable, desc.morphTypeColumn)), desc.morphValue)
			statement := legacy.Session(&gormio.Session{DryRun: true}).Find(c.dest)
			assert.NoError(t, statement.Error)
			assert.Equal(t, normalize(statement.Statement.SQL.String()), normalize(recorded.sql),
				"the loader's compiled child must render the predicate the GORM statement did")
		})
	}

	// The loader passes the foreign key and the morph type column as required columns, so both survive
	// column pruning: the dictionary is keyed off the foreign key and the type filter stays on the
	// statement.
	t.Run("pruned select keeps the foreign key and the morph type column", func(t *testing.T) {
		q := newRelQueryWith(t, &relUser{})
		desc, err := resolveRelation(q.instance, &relUser{}, "Houses")
		assert.NoError(t, err)
		if err != nil {
			return
		}
		ref := desc.references[0]

		child := q.relatedChild(desc.relatedTable, desc.relatedTable+"."+ref.foreignColumn, []any{uint(9)}).
			Where(desc.relatedTable+"."+desc.morphTypeColumn, desc.morphValue).(*Query)
		conditions := child.conditions
		conditions.selectItems = []any{"address", ref.foreignColumn, desc.morphTypeColumn}

		compiled, bindings, err := child.setConditions(conditions).compileSelect()
		assert.NoError(t, err)
		assert.Equal(t, `select "address", "houseable_id", "houseable_type" from "rel_houses" where "rel_houses"."houseable_id" in (?) and "rel_houses"."houseable_type" = ?`, compiled)
		assert.Equal(t, []any{uint(9), desc.morphValue}, bindings)
	})

	// The child addresses the related table alone, with no model of its own, so it inherits none of
	// the parent query's state — the same shape the fresh GORM session had. A related model's
	// soft-delete scope and global scopes therefore stay off the eager load.
	t.Run("child carries no inherited conditions", func(t *testing.T) {
		q := newRelQueryWith(t, &relUser{})
		desc, err := resolveRelation(q.instance, &relUser{}, "Houses")
		assert.NoError(t, err)
		if err != nil {
			return
		}
		ref := desc.references[0]

		child := q.relatedChild(desc.relatedTable, desc.relatedTable+"."+ref.foreignColumn, []any{uint(9)}).
			Where(desc.relatedTable+"."+desc.morphTypeColumn, desc.morphValue).(*Query)
		assert.Nil(t, child.conditions.model)
		assert.Nil(t, child.conditions.dest)
		assert.False(t, child.conditions.withTrashed)
		assert.Empty(t, child.conditions.withoutGlobalScopes)
		assert.Empty(t, child.conditions.scopes)
		assert.Len(t, child.conditions.where, 2)
	})
}

// --- loadMorphTo -----------------------------------------------------------

// morphOwnerKeyImage declares a MorphTo whose owner key is not the related table's "id", so the
// per-bucket child query can be pinned against the OwnerKey branch the loader takes.
type morphOwnerKeyImage struct {
	ID            uint
	Kind          string
	ImageableUUID string
	Imageable     any `gorm:"-"`
}

func (morphOwnerKeyImage) TableName() string { return "morph_owner_key_images" }

func (morphOwnerKeyImage) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Imageable": contractsorm.MorphTo{
			Name:       "imageable",
			TypeColumn: "kind",
			IDColumn:   "imageable_uuid",
			OwnerKey:   "uuid",
		},
	}
}

// morphOwnerKeyPost is the related model the custom-owner-key MorphTo resolves to. Its owner key is
// "uuid" rather than "id", which the loader requires the related model to declare: the eager loader
// keys each bucket off the related model's owner-key field, so a MorphTo with OwnerKey: "uuid" needs
// a related model carrying that column — a fact only visible once the loader itself is driven.
type morphOwnerKeyPost struct {
	ID    uint
	UUID  string
	Title string
}

func (morphOwnerKeyPost) TableName() string { return "morph_parent_like_posts" }

// morphScopedImage declares a MorphTo carrying an OnQuery scope, which loadMorphTo propagates into
// every per-type bucket descriptor so a scope declared once applies to each resolved table.
type morphScopedImage struct {
	ID            uint
	ImageableID   uint
	ImageableType string
	Imageable     any `gorm:"-"`
}

func (morphScopedImage) TableName() string { return "morph_scoped_images" }

func (morphScopedImage) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Imageable": contractsorm.MorphTo{
			Name: "imageable",
			OnQuery: func(q contractsorm.Query) contractsorm.Query {
				return q.Where("published", true)
			},
		},
	}
}

// TestLoadMorphTo_BucketChildQueryIsCompiled drives loadMorphTo itself for one morph bucket: it
// calls the loader with parent rows carrying the morph type and the owner-key value, and asserts the
// statement the loader built and executed — the type's own table filtered by its owner key, addressed
// through relatedChild so the framework compiler renders the statement instead of GORM.
//
// MorphTo resolves its related table per row from the *_type column, so the loader runs one query per
// bucket and each case pins the SQL one bucket produces. The GORM statement the loader used to hand
// over is rendered alongside and compared after normalisation, and the bindings the driver received
// are asserted exactly: same key type, same order, same arity.
func TestLoadMorphTo_BucketChildQueryIsCompiled(t *testing.T) {
	morphmap.Reset()
	defer morphmap.Reset()
	morphmap.Register(map[string]any{
		"post":      &morphParentLikePost{},
		"post_uuid": &morphOwnerKeyPost{},
		"image":     &relLogo{},
	})

	// Case and keyword spacing differ between the two builders; the predicate does not.
	normalize := func(sql string) string {
		sql = strings.Join(strings.Fields(strings.ToLower(sql)), " ")
		return strings.ReplaceAll(sql, ", ", ",")
	}

	cases := []struct {
		name     string
		parent   any
		relation string
		dest     any
		morph    string
		table    string
		ownerKey string
		chunk    []any
		parents  []reflect.Value
		sql      string
	}{
		{
			name:     "default owner key",
			parent:   &morphImage{},
			relation: "Imageable",
			dest:     &[]*morphParentLikePost{},
			morph:    "post",
			table:    "morph_parent_like_posts",
			ownerKey: "id",
			chunk:    []any{uint(5), uint(6)},
			parents: []reflect.Value{
				reflect.ValueOf(&morphImage{ImageableType: "post", ImageableID: 5}).Elem(),
				reflect.ValueOf(&morphImage{ImageableType: "post", ImageableID: 6}).Elem(),
			},
			sql: `select * from "morph_parent_like_posts" where "morph_parent_like_posts"."id" in (?, ?)`,
		},
		{
			name:     "another morph type resolves another table",
			parent:   &morphImage{},
			relation: "Imageable",
			dest:     &[]*relLogo{},
			morph:    "image",
			table:    "rel_logos",
			ownerKey: "id",
			chunk:    []any{uint(9)},
			parents: []reflect.Value{
				reflect.ValueOf(&morphImage{ImageableType: "image", ImageableID: 9}).Elem(),
			},
			sql: `select * from "rel_logos" where "rel_logos"."id" in (?)`,
		},
		{
			name:     "custom owner key",
			parent:   &morphOwnerKeyImage{},
			relation: "Imageable",
			dest:     &[]*morphOwnerKeyPost{},
			morph:    "post_uuid",
			table:    "morph_parent_like_posts",
			ownerKey: "uuid",
			chunk:    []any{"abc"},
			parents: []reflect.Value{
				reflect.ValueOf(&morphOwnerKeyImage{Kind: "post_uuid", ImageableUUID: "abc"}).Elem(),
			},
			sql: `select * from "morph_parent_like_posts" where "morph_parent_like_posts"."uuid" in (?)`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q, recorder := newRecordingRelQuery(t, c.parent)
			desc, err := resolveRelation(q.instance, c.parent, c.relation)
			assert.NoError(t, err)
			if err != nil {
				return
			}

			assert.Equal(t, relKindMorphTo, desc.kind)
			assert.Nil(t, desc.relatedModel, "MorphTo resolves its model per row, not at descriptor time")

			sample := morphmap.Find(c.morph)
			assert.NotNil(t, sample)
			relatedTable, err := tableNameFor(q.instance, sample)
			assert.NoError(t, err)
			assert.Equal(t, c.table, relatedTable)

			// The loader's owner key: the descriptor's, defaulted to "id".
			ownerKey := desc.morphOwnerKey
			if ownerKey == "" {
				ownerKey = "id"
			}
			assert.Equal(t, c.ownerKey, ownerKey)

			// Drive the loader itself, so the assertion is on the statement the loader built and ran
			// rather than on a child this test rebuilt by hand. The parents carry the morph type and
			// the owner-key value the loader buckets on.
			assert.NoError(t, q.loadMorphTo(c.parents, c.parent, desc, eagerLoadEntry{relation: c.relation}, nil))

			recorded := recorder.last(t)
			assert.Equal(t, c.sql, recorded.sql)
			assert.Equal(t, driverBindings(c.chunk), recorded.args)

			// Keep the legacy-versus-compiled comparison: the statement the loader executed must
			// render the same predicate the GORM chain it replaced produced.
			legacy := q.freshSession().Table(relatedTable).
				Where(fmt.Sprintf("%s IN ?", q.ident(relatedTable, ownerKey)), c.chunk)
			statement := legacy.Session(&gormio.Session{DryRun: true}).Find(c.dest)
			assert.NoError(t, statement.Error)
			assert.Equal(t, normalize(statement.Statement.SQL.String()), normalize(recorded.sql),
				"the loader's compiled child must render the predicate the GORM statement did")
		})
	}

	// The descriptor's OnQuery reaches the bucket child, so a scope declared once on the MorphTo
	// filters each resolved type's rows without the loader restating it per bucket.
	t.Run("bucket child receives the relation scope", func(t *testing.T) {
		q := newRelQueryWith(t, &morphScopedImage{})
		desc, err := resolveRelation(q.instance, &morphScopedImage{}, "Imageable")
		assert.NoError(t, err)
		if err != nil {
			return
		}
		assert.NotNil(t, desc.onQuery)

		child := q.relatedChild("morph_parent_like_posts", "morph_parent_like_posts.id", []any{uint(5)})
		scoped, ok := desc.onQuery(child).(*Query)
		assert.True(t, ok)
		assert.Len(t, scoped.conditions.where, 2, "owner-key IN plus the relation scope")

		compiled, bindings, err := scoped.compileSelect()
		assert.NoError(t, err)
		assert.Equal(t, `select * from "morph_parent_like_posts" where "morph_parent_like_posts"."id" in (?) and "published" = ?`, compiled)
		assert.Equal(t, []any{uint(5), true}, bindings)
	})

	// The loader passes the owner key as a required column, so it survives column pruning and the
	// bucket dictionary can be keyed off the related rows.
	t.Run("pruned select keeps the owner key", func(t *testing.T) {
		q := newRelQueryWith(t, &morphImage{})
		child := q.relatedChild("morph_parent_like_posts", "morph_parent_like_posts.id", []any{uint(5)})
		conditions := child.conditions
		conditions.selectItems = []any{"title", "id"}

		compiled, bindings, err := child.setConditions(conditions).compileSelect()
		assert.NoError(t, err)
		assert.Equal(t, `select "title", "id" from "morph_parent_like_posts" where "morph_parent_like_posts"."id" in (?)`, compiled)
		assert.Equal(t, []any{uint(5)}, bindings)
	})

	// Each bucket's child addresses the resolved table alone, with no model of its own — the same
	// shape the fresh GORM session had — so it inherits none of the parent query's state and a
	// related model's soft-delete scope and global scopes stay off the eager load.
	t.Run("bucket child carries no inherited conditions", func(t *testing.T) {
		q := newRelQueryWith(t, &morphImage{})
		child := q.relatedChild("morph_parent_like_posts", "morph_parent_like_posts.id", []any{uint(5)})
		assert.Nil(t, child.conditions.model)
		assert.Nil(t, child.conditions.dest)
		assert.False(t, child.conditions.withTrashed)
		assert.Empty(t, child.conditions.withoutGlobalScopes)
		assert.Empty(t, child.conditions.scopes)
		assert.Len(t, child.conditions.where, 1)
	})
}

// --- loadMany2Many ---------------------------------------------------------

// m2mPivotParent declares a Many2Many whose related model carries a Pivot struct. It exists so the
// select list the loader assembles — the two FK columns plus every column the hydration plan reports —
// can be pinned against the statement the legacy GORM chain produced, including the FK columns the
// plan reports a second time.
type m2mPivotParent struct {
	ID    uint
	Roles []*roleWithPivot `gorm:"-"`
}

func (m2mPivotParent) TableName() string { return "m2m_pivot_parents" }

func (m2mPivotParent) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Roles": contractsorm.Many2Many{Related: &roleWithPivot{}, Table: "m2m_pivot_parent_roles"},
	}
}

// TestLoadMany2Many_ReadsAreCompiled pins both queries loadMany2Many builds: the pivot-table read
// and the related-rows read, each addressed through the package's child builders so the framework
// compiler renders the statement instead of GORM. The loader is driven with parent keys too, so a
// change to either half of its body fails the test.
//
// The pivot read is the delicate one. Its columns come back as a column-keyed map that the hydration
// loop indexes by the bare foreign-key names, and the relation's OnPivotQuery scope has to reach the
// statement; both are pinned here. Each case also renders the GORM chain the loader used to hand over
// and compares the two after normalisation, so the conversion cannot drift from the SQL it replaced,
// and the binding slice is asserted exactly: same key type, same order, same arity.
func TestLoadMany2Many_ReadsAreCompiled(t *testing.T) {
	// Case, keyword spacing and identifier quoting differ between the two builders; the predicate
	// does not. The legacy chain leaves the pivot select list bare — GORM reads a bare Select string
	// as an expression — where the compiled statement quotes each identifier. Those are the same
	// identifiers, and the driver reports the same column labels back, which is what the pivot map is
	// keyed by, so quotes are dropped from both sides here. The compiled statement is asserted
	// verbatim above, and the dots survive below, so a quoting or qualification regression still
	// fails rather than being normalised away.
	normalize := func(sql string) string {
		sql = strings.Join(strings.Fields(strings.ToLower(sql)), " ")
		sql = strings.ReplaceAll(sql, ", ", ",")

		return strings.ReplaceAll(sql, `"`, "")
	}

	t.Run("pivot read without a pivot scope", func(t *testing.T) {
		q := newRelQueryWith(t, &relUser{})
		desc, err := resolveRelation(q.instance, &relUser{}, "Roles")
		assert.NoError(t, err)
		if err != nil {
			return
		}

		assert.Equal(t, relKindMany2Many, desc.kind)
		assert.Equal(t, "rel_user_roles", desc.pivotTable)
		assert.Equal(t, "rel_user_id", desc.pivotParentRef.foreignColumn)
		assert.Equal(t, "rel_role_id", desc.pivotRelatedRef.foreignColumn)

		cols := []string{desc.pivotParentRef.foreignColumn, desc.pivotRelatedRef.foreignColumn}
		chunk := []any{uint(1), uint(2)}

		// The child is named the way the loader names it: the pivot table verbatim, the IN column
		// unquoted and table-qualified, and the select list kept bare so the returned column names —
		// which are the map keys the hydration loop indexes — stay the FK names.
		child := q.pivotChild(desc.pivotTable, desc.pivotTable+"."+desc.pivotParentRef.foreignColumn, chunk, cols)
		compiled, bindings, err := child.compileSelect()
		assert.NoError(t, err)
		assert.Equal(t,
			`select "rel_user_id", "rel_role_id" from "rel_user_roles" where "rel_user_roles"."rel_user_id" in (?, ?)`,
			compiled)
		assert.Equal(t, chunk, bindings)

		// No model is attached, so the child starts from the pivot table alone and inherits none of
		// the parent query's state — the same shape the fresh GORM session had.
		assert.Nil(t, child.conditions.model)
		assert.Nil(t, child.conditions.dest)
		assert.False(t, child.conditions.withTrashed)
		assert.Empty(t, child.conditions.withoutGlobalScopes)
		assert.Empty(t, child.conditions.scopes)
		assert.Len(t, child.conditions.where, 1)
		assert.Len(t, child.conditions.selectItems, 2)
	})

	t.Run("pivot read receives the relation scope and the hydration plan's columns", func(t *testing.T) {
		q := newRelQueryWith(t, &scopeUser{})
		desc, err := resolveRelation(q.instance, &scopeUser{}, "Roles")
		assert.NoError(t, err)
		if err != nil {
			return
		}
		assert.NotNil(t, desc.onPivotQuery, "the fixture declares OnePivotQuery")

		// The loader's select list is the two FK columns plus whatever the hydration plan reports.
		plan, err := preparePivotHydration(q, desc)
		assert.NoError(t, err)
		cols := []string{desc.pivotParentRef.foreignColumn, desc.pivotRelatedRef.foreignColumn}
		if plan != nil {
			cols = append(cols, plan.extraColumns...)
		}
		chunk := []any{uint(1), uint(2)}

		child := q.pivotChild(desc.pivotTable, desc.pivotTable+"."+desc.pivotParentRef.foreignColumn, chunk, cols)
		scoped, ok := applyOnPivotQueryToQuery(q.queryGrammar, child, desc).(*Query)
		assert.True(t, ok)

		compiled, bindings, err := scoped.compileSelect()
		assert.NoError(t, err)
		assert.Equal(t,
			`select "scope_user_id", "scope_role_id" from "scope_user_roles" where "scope_user_roles"."scope_user_id" in (?, ?) and "scope_user_roles"."active" = ?`,
			compiled)
		// The chunk's keys first, then the scope's own binding.
		assert.Equal(t, []any{uint(1), uint(2), 1}, bindings)
	})

	t.Run("pivot read keeps the hydration plan's columns, duplicates included", func(t *testing.T) {
		q := newRelQueryWith(t, &m2mPivotParent{})
		desc, err := resolveRelation(q.instance, &m2mPivotParent{}, "Roles")
		assert.NoError(t, err)
		if err != nil {
			return
		}

		plan, err := preparePivotHydration(q, desc)
		assert.NoError(t, err)
		assert.NotNil(t, plan, "the related model declares a Pivot struct")
		assert.ElementsMatch(t, []string{"user_id", "role_id", "priority", "notes"}, plan.extraColumns)

		// The loader's list: the two FK columns first, then every column the plan reports. Because the
		// plan reports the pivot struct's own columns, the FK columns appear a second time — exactly the
		// list the loader used to hand to GORM's Select, and the compiled child must keep it: it sets the
		// select list on the conditions directly, so nothing dedupes it the way Query.Select would.
		cols := []string{desc.pivotParentRef.foreignColumn, desc.pivotRelatedRef.foreignColumn}
		cols = append(cols, plan.extraColumns...)
		assert.Equal(t, []string{desc.pivotParentRef.foreignColumn, desc.pivotRelatedRef.foreignColumn}, cols[:2])
		assert.Len(t, cols, 6, "two FK columns plus the pivot struct's four")
		assert.Equal(t, desc.pivotParentRef.foreignColumn, "m2m_pivot_parent_id")
		assert.Equal(t, desc.pivotRelatedRef.foreignColumn, "role_with_pivot_id")

		chunk := []any{uint(1)}
		child := q.pivotChild(desc.pivotTable, desc.pivotTable+"."+desc.pivotParentRef.foreignColumn, chunk, cols)
		compiled, bindings, err := child.compileSelect()
		assert.NoError(t, err)

		// Built from the descriptor's own values, so the expectation cannot encode a wrong name or order.
		quoted := make([]string, 0, len(cols))
		for _, col := range cols {
			quoted = append(quoted, `"`+col+`"`)
		}
		expected := "select " + strings.Join(quoted, ", ") +
			" from \"" + desc.pivotTable + "\" where \"" + desc.pivotTable + "\".\"" +
			desc.pivotParentRef.foreignColumn + "\" in (?)"
		assert.Equal(t, expected, compiled)
		assert.Equal(t, chunk, bindings)

	})

	t.Run("related read is compiled and keeps the related key when pruned", func(t *testing.T) {
		q := newRelQueryWith(t, &relUser{})
		desc, err := resolveRelation(q.instance, &relUser{}, "Roles")
		assert.NoError(t, err)
		if err != nil {
			return
		}

		chunk := []any{uint(9)}
		child := q.relatedChild(desc.relatedTable, desc.relatedTable+"."+desc.pivotRelatedRef.primaryColumn, chunk)
		compiled, bindings, err := child.compileSelect()
		assert.NoError(t, err)
		assert.Equal(t, `select * from "rel_roles" where "rel_roles"."id" in (?)`, compiled)
		assert.Equal(t, chunk, bindings)

		legacy := q.freshSession().
			Table(desc.relatedTable).
			Where(fmt.Sprintf("%s IN ?", q.ident(desc.relatedTable, desc.pivotRelatedRef.primaryColumn)), chunk)
		statement := legacy.Session(&gormio.Session{DryRun: true}).Find(&[]*relRole{})
		assert.NoError(t, statement.Error)
		assert.Equal(t, normalize(statement.Statement.SQL.String()), normalize(compiled),
			"the compiled related read must render the predicate the GORM chain did")
		assert.Equal(t, statement.Statement.Vars, bindings)

		// The loader passes the related key as a required column, so it survives column pruning and
		// the dictionary can be keyed off the related rows.
		conditions := child.conditions
		conditions.selectItems = []any{"name", desc.pivotRelatedRef.primaryColumn}
		pruned, prunedBindings, err := child.setConditions(conditions).compileSelect()
		assert.NoError(t, err)
		assert.Equal(t, `select "name", "id" from "rel_roles" where "rel_roles"."id" in (?)`, pruned)
		assert.Equal(t, chunk, prunedBindings)
	})

	// No parent keys: the loader takes its empty path and never reaches a statement at all, so the
	// relation field is cleared rather than queried for an IN list that cannot match.
	t.Run("no parent keys issues no statement", func(t *testing.T) {
		q := newRelQueryWith(t, &relUser{})
		desc, err := resolveRelation(q.instance, &relUser{}, "Roles")
		assert.NoError(t, err)
		if err != nil {
			return
		}

		assert.NoError(t, q.loadMany2Many(nil, &relUser{}, desc, eagerLoadEntry{relation: "Roles"}, nil))
	})
	// Drive the loader itself with parent keys. The recording driver hands back one pivot row,
	// which is what makes loadMany2Many run its second read, so the assertion lands on the
	// statements the loader built and executed rather than on children this test rebuilt by hand.
	t.Run("loader reads the pivot table then the related table", func(t *testing.T) {
		q, recorder := newRecordingRelQuery(t, &relUser{})
		desc, err := resolveRelation(q.instance, &relUser{}, "Roles")
		assert.NoError(t, err)
		if err != nil {
			return
		}

		// One pivot row links parent 1 to related 9; the loader then reads the related row for key 9.
		recorder.returnRows(`from "`+desc.pivotTable+`"`,
			[]string{desc.pivotParentRef.foreignColumn, desc.pivotRelatedRef.foreignColumn},
			[]driver.Value{int64(1), int64(9)})

		parents := []reflect.Value{
			reflect.ValueOf(&relUser{ID: 1}).Elem(),
			reflect.ValueOf(&relUser{ID: 2}).Elem(),
		}

		assert.NoError(t, q.loadMany2Many(parents, &relUser{}, desc, eagerLoadEntry{relation: "Roles"}, nil))
		assert.Equal(t, 2, recorder.count(), "the loader reads the pivot table and then the related table")

		pivot := recorder.at(t, 0)
		assert.Equal(t,
			`select "rel_user_id", "rel_role_id" from "rel_user_roles" where "rel_user_roles"."rel_user_id" in (?, ?)`,
			pivot.sql)
		assert.Equal(t, driverBindings([]any{uint(1), uint(2)}), pivot.args)

		related := recorder.at(t, 1)
		assert.Equal(t, `select * from "rel_roles" where "rel_roles"."id" in (?)`, related.sql)
		// The key comes off the pivot row as the driver-typed int64, not the uint the parents carried.
		assert.Equal(t, []driver.Value{int64(9)}, related.args)
	})

	// The relation's OnPivotQuery scope must reach the statement the loader actually runs, not just
	// the child this test could rebuild: findMapsCompiled threads it in before compiling.
	t.Run("loader's pivot read carries the relation scope", func(t *testing.T) {
		q, recorder := newRecordingRelQuery(t, &scopeUser{})
		desc, err := resolveRelation(q.instance, &scopeUser{}, "Roles")
		assert.NoError(t, err)
		if err != nil {
			return
		}
		assert.NotNil(t, desc.onPivotQuery, "the fixture declares OnPivotQuery")

		recorder.returnRows(`from "`+desc.pivotTable+`"`,
			[]string{desc.pivotParentRef.foreignColumn, desc.pivotRelatedRef.foreignColumn},
			[]driver.Value{int64(1), int64(9)})

		parents := []reflect.Value{
			reflect.ValueOf(&scopeUser{ID: 1}).Elem(),
			reflect.ValueOf(&scopeUser{ID: 2}).Elem(),
		}

		assert.NoError(t, q.loadMany2Many(parents, &scopeUser{}, desc, eagerLoadEntry{relation: "Roles"}, nil))

		pivot := recorder.at(t, 0)
		assert.Equal(t,
			`select "scope_user_id", "scope_role_id" from "scope_user_roles" where "scope_user_roles"."scope_user_id" in (?, ?) and "scope_user_roles"."active" = ?`,
			pivot.sql)
		// The chunk's keys first, then the scope's own binding.
		assert.Equal(t, []driver.Value{int64(1), int64(2), int64(1)}, pivot.args)

		// The scope never dropped the row, so the loader still ran the related read off it.
		related := recorder.at(t, 1)
		assert.Equal(t, `select * from "scope_roles" where "scope_roles"."id" in (?)`, related.sql)
		assert.Equal(t, []driver.Value{int64(9)}, related.args)
	})
}

// --- Hydration: the rows a loader reads land on the parents -----------------

// pivotUser is the parent fixture for the many-to-many hydration test: a user whose Roles relation
// points at roleWithPivot, the related model that declares a Pivot field. Its table name is what the
// pivot's parent column is derived from, so the canned pivot rows are keyed by "pivot_user_id".
type pivotUser struct {
	ID    uint
	Name  string
	Roles []*roleWithPivot `gorm:"-"`
}

func (pivotUser) TableName() string { return "pivot_users" }

func (pivotUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Roles": contractsorm.Many2Many{Related: &roleWithPivot{}, Table: "rel_user_pivot_roles"},
	}
}

// TestLoadHasOneOrMany_AssignsHydratedRows drives the loader with a result set rather than an empty
// one, so the dictionary assembly and assignToParents run against real hydrated rows. The statement
// tests around this one pin what the loader asked for; this one pins what it did with the answer:
// each parent gets its own related row, a parent the result set skipped is left empty instead of
// borrowing a neighbour's row, and a HasOne keeps only the first of several matches.
func TestLoadHasOneOrMany_AssignsHydratedRows(t *testing.T) {
	t.Run("has many assigns per parent", func(t *testing.T) {
		q, recorder := newRecordingRelQuery(t, &relUser{})
		desc, err := resolveRelation(q.instance, &relUser{}, "Books")
		assert.NoError(t, err)
		if err != nil {
			return
		}
		assert.Equal(t, relKindHasMany, desc.kind)

		// Two books for user 1, one for user 2, and none for the user the result set omits.
		recorder.returnRows(`from "rel_books"`, []string{"id", "title", "user_id"},
			[]driver.Value{int64(10), "first", int64(1)},
			[]driver.Value{int64(11), "second", int64(1)},
			[]driver.Value{int64(12), "third", int64(2)},
		)

		user1 := &relUser{ID: 1}
		user2 := &relUser{ID: 2}
		user3 := &relUser{ID: 3}
		parents := []reflect.Value{
			reflect.ValueOf(user1).Elem(),
			reflect.ValueOf(user2).Elem(),
			reflect.ValueOf(user3).Elem(),
		}

		assert.NoError(t, q.loadHasOneOrMany(parents, &relUser{}, desc, eagerLoadEntry{relation: "Books"}, nil, true))

		assert.Len(t, user1.Books, 2)
		assert.Equal(t, uint(10), user1.Books[0].ID)
		assert.Equal(t, "second", user1.Books[1].Title)
		assert.Len(t, user2.Books, 1)
		assert.Equal(t, "third", user2.Books[0].Title)
		assert.NotNil(t, user3.Books, "a has-many parent with no match gets an empty slice, not the zero value")
		assert.Empty(t, user3.Books, "a parent the result set skipped must not borrow another parent's rows")
	})

	t.Run("has one keeps the first match", func(t *testing.T) {
		q, recorder := newRecordingRelQuery(t, &relUser{})
		desc, err := resolveRelation(q.instance, &relUser{}, "Profile")
		assert.NoError(t, err)
		if err != nil {
			return
		}
		assert.Equal(t, relKindHasOne, desc.kind)

		// Two rows for the same parent: a HasOne keeps one, and it is the first in result order.
		recorder.returnRows(`from "rel_profiles"`, []string{"id", "bio", "user_id"},
			[]driver.Value{int64(20), "first bio", int64(1)},
			[]driver.Value{int64(21), "second bio", int64(1)},
		)

		user := &relUser{ID: 1}
		parents := []reflect.Value{reflect.ValueOf(user).Elem()}

		assert.NoError(t, q.loadHasOneOrMany(parents, &relUser{}, desc, eagerLoadEntry{relation: "Profile"}, nil, false))

		assert.NotNil(t, user.Profile)
		assert.Equal(t, uint(20), user.Profile.ID)
		assert.Equal(t, "first bio", user.Profile.Bio, "a has-one keeps the first row the result set returned")
	})

	t.Run("belongs to matches the foreign key and clears an unmatched parent", func(t *testing.T) {
		q, recorder := newRecordingRelQuery(t, &relBook{})
		desc, err := resolveRelation(q.instance, &relBook{}, "Author")
		assert.NoError(t, err)
		if err != nil {
			return
		}
		assert.Equal(t, relKindBelongsTo, desc.kind)

		// The dictionary is keyed by the related primary key, which is what the parent's FK matches.
		recorder.returnRows(`from "rel_users"`, []string{"id", "name"},
			[]driver.Value{int64(5), "Ada"},
			[]driver.Value{int64(6), "Grace"},
		)

		book5 := &relBook{ID: 1, AuthorID: 5}
		book6 := &relBook{ID: 2, AuthorID: 6}
		// No author key at all: the loader skips this parent rather than querying for it.
		bookNone := &relBook{ID: 3}
		// A key nothing matched: the relation field is cleared, and the parent is not skipped.
		bookMissing := &relBook{ID: 4, AuthorID: 7, Author: &relUser{Name: "stale"}}
		parents := []reflect.Value{
			reflect.ValueOf(book5).Elem(),
			reflect.ValueOf(book6).Elem(),
			reflect.ValueOf(bookNone).Elem(),
			reflect.ValueOf(bookMissing).Elem(),
		}

		assert.NoError(t, q.loadBelongsTo(parents, &relBook{}, desc, eagerLoadEntry{relation: "Author"}, nil))

		assert.NotNil(t, book5.Author)
		assert.Equal(t, "Ada", book5.Author.Name)
		assert.NotNil(t, book6.Author)
		assert.Equal(t, "Grace", book6.Author.Name)
		assert.Nil(t, bookNone.Author, "a parent without an owner key is left untouched")
		assert.Nil(t, bookMissing.Author, "a parent whose key matched no row has its relation cleared")
	})
}

// TestLoadMany2Many_HydratesPivotThroughTheLoader drives loadMany2Many end to end with canned pivot
// and related rows, so the pivot-into-Pivot hydration path runs through the loader rather than
// through writePivotField on its own: each parent's related row carries the pivot attributes of its
// own pivot row, and two parents sharing one related row get distinct instances.
func TestLoadMany2Many_HydratesPivotThroughTheLoader(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &pivotUser{})
	desc, err := resolveRelation(q.instance, &pivotUser{}, "Roles")
	assert.NoError(t, err)
	if err != nil {
		return
	}
	assert.Equal(t, relKindMany2Many, desc.kind)
	assert.Equal(t, "Pivot", desc.pivotField)

	pivotParentCol := desc.pivotParentRef.foreignColumn
	pivotRelatedCol := desc.pivotRelatedRef.foreignColumn

	// Both parents pivot-attach the same role 99, each with its own priority and notes. The pivot
	// columns are the FK columns plus the Pivot struct's own fields, in the order the plan reports
	// them — the same list the loader put in the SELECT above.
	recorder.returnRows(`from "`+desc.pivotTable+`"`,
		[]string{pivotParentCol, pivotRelatedCol, "user_id", "role_id", "priority", "notes"},
		[]driver.Value{int64(1), int64(99), int64(1), int64(99), "high", "one"},
		[]driver.Value{int64(2), int64(99), int64(2), int64(99), "low", "two"},
	)
	recorder.returnRows(`from "`+desc.relatedTable+`"`,
		[]string{"id", "name"},
		[]driver.Value{int64(99), "admin"},
	)

	user1 := &pivotUser{ID: 1}
	user2 := &pivotUser{ID: 2}
	parents := []reflect.Value{reflect.ValueOf(user1).Elem(), reflect.ValueOf(user2).Elem()}

	assert.NoError(t, q.loadMany2Many(parents, &pivotUser{}, desc, eagerLoadEntry{relation: "Roles"}, nil))

	assert.Equal(t, 2, recorder.count(), "the loader reads the pivot table and then the related table")
	pivot := recorder.at(t, 0)
	assert.Equal(t,
		`select "`+pivotParentCol+`", "`+pivotRelatedCol+`", "user_id", "role_id", "priority", "notes" from "`+desc.pivotTable+
			`" where "`+desc.pivotTable+`"."`+pivotParentCol+`" in (?, ?)`,
		pivot.sql)
	assert.Equal(t, driverBindings([]any{uint(1), uint(2)}), pivot.args)

	related := recorder.at(t, 1)
	assert.Equal(t, `select * from "`+desc.relatedTable+`" where "`+desc.relatedTable+`"."id" in (?)`, related.sql)
	assert.Equal(t, []driver.Value{int64(99)}, related.args)

	if !assert.Len(t, user1.Roles, 1) || !assert.Len(t, user2.Roles, 1) {
		return
	}
	role1, role2 := user1.Roles[0], user2.Roles[0]
	assert.Equal(t, "admin", role1.Name)
	assert.NotSame(t, role1, role2, "each parent must get its own related-row instance when a Pivot field is in play")
	assert.Equal(t, "high", role1.Pivot.Priority)
	assert.Equal(t, "one", role1.Pivot.Notes)
	assert.Equal(t, "low", role2.Pivot.Priority)
	assert.Equal(t, "two", role2.Pivot.Notes)
}

// --- Chunking --------------------------------------------------------------

// TestLoadRelatedRows_ChunkedPerChunkSize drives a loader whose key set is larger than the configured
// chunk size, so runChunkedRelatedQueryCompiled runs its per-chunk loop: one statement per chunk, the
// keys split in order, and every chunk's rows assigned back to the parents that own them.
func TestLoadRelatedRows_ChunkedPerChunkSize(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &relUser{})

	// The eager-load IN list is split at the configured size; without this the loader stays on the
	// single-statement path and the loop below never runs twice.
	cfg := mocksconfig.NewConfig(t)
	cfg.EXPECT().GetInt("database.eager_load_chunk_size", mock.Anything).Return(2)
	q.config = cfg

	desc, err := resolveRelation(q.instance, &relUser{}, "Books")
	assert.NoError(t, err)
	if err != nil {
		return
	}

	// One book per parent, keyed by the parent id the chunk carries.
	recorder.returnRows(`"rel_books"."user_id" in (?, ?)`, []string{"id", "title", "user_id"},
		[]driver.Value{int64(10), "one", int64(1)},
		[]driver.Value{int64(11), "two", int64(2)},
	)
	recorder.returnRows(`"rel_books"."user_id" in (?)`, []string{"id", "title", "user_id"},
		[]driver.Value{int64(12), "three", int64(3)},
	)

	user1 := &relUser{ID: 1}
	user2 := &relUser{ID: 2}
	user3 := &relUser{ID: 3}
	parents := []reflect.Value{
		reflect.ValueOf(user1).Elem(),
		reflect.ValueOf(user2).Elem(),
		reflect.ValueOf(user3).Elem(),
	}

	assert.NoError(t, q.loadHasOneOrMany(parents, &relUser{}, desc, eagerLoadEntry{relation: "Books"}, nil, true))

	assert.Equal(t, 2, recorder.count(), "three keys at a chunk size of two are two statements")
	first := recorder.at(t, 0)
	assert.Equal(t, `select * from "rel_books" where "rel_books"."user_id" in (?, ?)`, first.sql)
	assert.Equal(t, driverBindings([]any{uint(1), uint(2)}), first.args)

	second := recorder.at(t, 1)
	assert.Equal(t, `select * from "rel_books" where "rel_books"."user_id" in (?)`, second.sql)
	assert.Equal(t, driverBindings([]any{uint(3)}), second.args)

	// Rows from every chunk land on the parent that owns them, not only on the last chunk's parents.
	assert.Len(t, user1.Books, 1)
	assert.Equal(t, "one", user1.Books[0].Title)
	assert.Len(t, user2.Books, 1)
	assert.Equal(t, "two", user2.Books[0].Title)
	assert.Len(t, user3.Books, 1)
	assert.Equal(t, "three", user3.Books[0].Title)
}

// --- Column pruning through the loader -------------------------------------

// TestLoadRelatedRows_PrunesToRequestedColumns drives a loader with a columns list — the shape
// With("Books:title") produces — so the pruning branch in runRelatedQueryCompiled runs and, with it,
// the rule that the key the loader keys its dictionary off survives the pruning.
func TestLoadRelatedRows_PrunesToRequestedColumns(t *testing.T) {
	t.Run("loader's own child", func(t *testing.T) {
		q, recorder := newRecordingRelQuery(t, &relUser{})
		desc, err := resolveRelation(q.instance, &relUser{}, "Books")
		assert.NoError(t, err)
		if err != nil {
			return
		}

		// The caller asked for title only; user_id is appended as the required key, after it.
		recorder.returnRows(`from "rel_books"`, []string{"title", "user_id"},
			[]driver.Value{"a", int64(1)},
		)
		user := &relUser{ID: 1}
		parents := []reflect.Value{reflect.ValueOf(user).Elem()}

		assert.NoError(t, q.loadHasOneOrMany(parents, &relUser{}, desc,
			eagerLoadEntry{relation: "Books", columns: []string{"title"}}, nil, true))

		recorded := recorder.last(t)
		assert.Equal(t, `select "title", "user_id" from "rel_books" where "rel_books"."user_id" in (?)`, recorded.sql)
		assert.Equal(t, driverBindings([]any{uint(1)}), recorded.args)
		// The pruned row still carried the key, so it was matched and assigned.
		if assert.Len(t, user.Books, 1) {
			assert.Equal(t, "a", user.Books[0].Title)
		}
	})

	t.Run("required key already present under a table qualifier", func(t *testing.T) {
		q, recorder := newRecordingRelQuery(t, &relUser{})
		desc, err := resolveRelation(q.instance, &relUser{}, "Books")
		assert.NoError(t, err)
		if err != nil {
			return
		}

		// A caller that names the key with its table does not get it appended a second time: the
		// suffix comparison is what recognises it.
		recorder.returnRows(`from "rel_books"`, []string{"title", "user_id"},
			[]driver.Value{"a", int64(1)},
		)
		user := &relUser{ID: 1}
		parents := []reflect.Value{reflect.ValueOf(user).Elem()}

		assert.NoError(t, q.loadHasOneOrMany(parents, &relUser{}, desc,
			eagerLoadEntry{relation: "Books", columns: []string{"rel_books.title", "rel_books.user_id"}}, nil, true))

		assert.Equal(t,
			`select "rel_books"."title", "rel_books"."user_id" from "rel_books" where "rel_books"."user_id" in (?)`,
			recorder.last(t).sql)
		if assert.Len(t, user.Books, 1) {
			assert.Equal(t, "a", user.Books[0].Title)
		}
	})

	t.Run("through With", func(t *testing.T) {
		q, recorder := newRecordingRelQuery(t, &relUser{})
		q = q.With("Books:title").(*Query)
		assert.True(t, q.conditions.eagerLoad[0].columns[0] == "title")

		recorder.returnRows(`from "rel_users"`, []string{"id", "name"},
			[]driver.Value{int64(1), "Ada"},
		)
		recorder.returnRows(`from "rel_books"`, []string{"title", "user_id"},
			[]driver.Value{"a", int64(1)},
		)

		var users []relUser
		assert.NoError(t, q.Get(&users))

		assert.Equal(t, 2, recorder.count())
		assert.Equal(t, `select * from "rel_users"`, recorder.at(t, 0).sql)
		assert.Equal(t,
			`select "title", "user_id" from "rel_books" where "rel_books"."user_id" in (?)`,
			recorder.at(t, 1).sql)
		if assert.Len(t, users, 1) && assert.Len(t, users[0].Books, 1) {
			assert.Equal(t, "a", users[0].Books[0].Title)
		}
	})
}

// TestAssignToParents covers the dictionary write itself, with the dictionary supplied rather than
// read from a driver. The hydration tests above drive it through two loaders; this one isolates the
// three rules the function states: a zero key is skipped (and, for a has-one, leaves the field
// alone), a has-one keeps only the first match, and a has-many keeps every match in result order.
func TestAssignToParents(t *testing.T) {
	q := newRelQuery(t)

	t.Run("zero key is skipped for a has-one", func(t *testing.T) {
		// The parent's owner key is zero, but the dictionary holds a row under the zero key's
		// spelling. A skipped parent must not take it: the loader never queried for that key.
		desc, err := resolveRelation(q.instance, &relUser{}, "Profile")
		assert.NoError(t, err)
		if err != nil {
			return
		}
		schema, err := parseSchema(q.instance, &relUser{})
		assert.NoError(t, err)
		parentField := schema.FieldsByDBName[desc.references[0].primaryColumn]
		assert.NotNil(t, parentField)

		user := &relUser{}
		parents := []reflect.Value{reflect.ValueOf(user).Elem()}
		dict := map[string][]reflect.Value{
			"0": {reflect.ValueOf(&relProfile{ID: 1, Bio: "belongs to nobody"})},
		}

		assert.NoError(t, q.assignToParents(parents, parentField, "Profile", dict, false))
		assert.Nil(t, user.Profile, "a parent with no owner key must not take a row keyed zero")
	})

	t.Run("zero key is skipped for a has-many", func(t *testing.T) {
		desc, err := resolveRelation(q.instance, &relUser{}, "Books")
		assert.NoError(t, err)
		if err != nil {
			return
		}
		schema, err := parseSchema(q.instance, &relUser{})
		assert.NoError(t, err)
		parentField := schema.FieldsByDBName[desc.references[0].primaryColumn]

		// The dictionary holds a row for user 1 only. This parent has no key, so it must not reach
		// the empty-slice branch for it: a has-many parent the query never asked about keeps the
		// field it had, and takes no row.
		user := &relUser{Books: []*relBook{{ID: 9, Title: "stale"}}}
		parents := []reflect.Value{reflect.ValueOf(user).Elem()}
		dict := map[string][]reflect.Value{
			"1": {reflect.ValueOf(&relBook{ID: 1, Title: "belongs to user 1"})},
		}

		assert.NoError(t, q.assignToParents(parents, parentField, "Books", dict, true))
		assert.Empty(t, user.Books, "a parent with no owner key must not take another parent's rows")
	})

	t.Run("has-one keeps the first match", func(t *testing.T) {
		// The relation field is slice-shaped here, which is the only shape the truncation is
		// observable on: a pointer field takes the first row either way. With isMany=false the
		// dictionary entry is cut to one row before the write, so the slice gets exactly one element
		// instead of the two the related read returned.
		desc, err := resolveRelation(q.instance, &relUser{}, "Books")
		assert.NoError(t, err)
		if err != nil {
			return
		}
		schema, err := parseSchema(q.instance, &relUser{})
		assert.NoError(t, err)
		parentField := schema.FieldsByDBName[desc.references[0].primaryColumn]

		user := &relUser{ID: 1}
		parents := []reflect.Value{reflect.ValueOf(user).Elem()}
		dict := map[string][]reflect.Value{
			"1": {
				reflect.ValueOf(&relBook{ID: 10, Title: "first"}),
				reflect.ValueOf(&relBook{ID: 11, Title: "second"}),
			},
		}

		assert.NoError(t, q.assignToParents(parents, parentField, "Books", dict, false))
		if assert.Len(t, user.Books, 1) {
			assert.Equal(t, uint(10), user.Books[0].ID, "a has-one keeps the first match")
		}
	})

	t.Run("has-many keeps every match in order", func(t *testing.T) {
		desc, err := resolveRelation(q.instance, &relUser{}, "Books")
		assert.NoError(t, err)
		if err != nil {
			return
		}
		schema, err := parseSchema(q.instance, &relUser{})
		assert.NoError(t, err)
		parentField := schema.FieldsByDBName[desc.references[0].primaryColumn]

		user := &relUser{ID: 1}
		parents := []reflect.Value{reflect.ValueOf(user).Elem()}
		dict := map[string][]reflect.Value{
			"1": {
				reflect.ValueOf(&relBook{ID: 10, Title: "first"}),
				reflect.ValueOf(&relBook{ID: 11, Title: "second"}),
			},
		}

		assert.NoError(t, q.assignToParents(parents, parentField, "Books", dict, true))
		if assert.Len(t, user.Books, 2) {
			assert.Equal(t, uint(10), user.Books[0].ID)
			assert.Equal(t, uint(11), user.Books[1].ID)
		}
	})
}
