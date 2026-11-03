package pivot

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	databasegorm "github.com/gradii/framework/database/gorm"
	relations "github.com/gradii/framework/database/gorm/relations"
	"github.com/gradii/framework/support/carbon"
)

// This file ports Laravel's tests/Database/DatabaseEloquentPivotTest.php (12.x) — the corpus of the
// Eloquent Pivot model class — onto the framework ORM: one Go test per Laravel test method, named
// TestPivot_<method without the test prefix>.
//
// The corpus is a DB-less PHPUnit TestCase: every test builds a Pivot (or a stub subclass) through
// Pivot::fromAttributes/fromRawAttributes over a mocked parent and asserts the Pivot object's own
// state — its attribute bag, mutator/cast pipeline, dirty tracking, per-instance $timestamps flag,
// pivot keys, table name, parent timestamp columns, and relation registry; the one query-shaped
// method (testDeleteMethodDeletesModelByKeys) drives a mocked builder, so the corpus compiles no
// SQL and has no schema for a port to create.
//
// The framework now carries the ported pivot model — relations.Pivot (database/gorm/relations/
// pivot.go: Illuminate Pivot + AsPivot, illuminate/database v12.55.1) — so this corpus drives the
// real type through the harness OrmBuilder. 11 of the 14 cases are flipped: the state cases assert
// the pivot instance's own facets, and the delete case asserts its behavioural reading against the
// harness sqlite database. The 3 remaining skips are PHP-only facets of the corpus's
// mutator/cast stubs, kept with the executed evidence quoted in the skip reasons.
//
// Upstream: the vendor tree ships no tests directory, so the case list was read from
// /Users/dog/OpenSource/framework/tests/Database/DatabaseEloquentPivotTest.php (the cited
// checkout; identical test-method set) and re-checked against the v12.55.1 source fetched from
// raw.githubusercontent.com/laravel/framework (only the mockery tearDown differs). A
// MorphPivotTest.php does not exist in either tree — the checkout's git ls-files lists none, the
// vendor tree ships no tests, and raw.githubusercontent.com answers 404 at every candidate path
// under v12.55.1 — so there is no MorphPivot corpus to port here; the MorphPivot surface is
// pinned by database/gorm/relations/morph_pivot_test.go.
//
// Oracle: php .claude/laravel-oracle/oracle.php (prints "illuminate/database: v12.55.1") executed
// /tmp/pivotflips/corpus_probe.php and /tmp/pivotflips/delete_probe.php for this flip; every
// assertion below quotes the probe lines it is pinned by. The divergences the Go type deliberately
// carries (registered in relations/pivot.go's class mapping) — attribute values are stored exactly
// as written (no date normalization), there is no mutator/cast pipeline, and there is no in-memory
// relation registry (the pivotParent/pivotRelated links are the pivot's related state) — are
// called out at the assertions they touch.

// --- fixtures --------------------------------------------------------------------------------

// pivotCorpusParent is the corpus's plain parent: a key plus the conventional timestamp columns
// every Eloquent model carries — the created-at column name the pivot's dynamic timestamps flag
// resolves through the parent link for the raw-attributes constructor.
type pivotCorpusParent struct {
	ID        uint             `gorm:"column:id;primaryKey"`
	CreatedAt *carbon.DateTime `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`
}

func (pivotCorpusParent) TableName() string { return "pivot_corpus_parents" }

// pivotCorpusConnectedParent declares the connection name the corpus's parent mock returns from
// getConnectionName() — the value AsPivot::fromAttributes stamps onto the pivot.
type pivotCorpusConnectedParent struct {
	ID uint `gorm:"column:id;primaryKey"`
}

func (pivotCorpusConnectedParent) TableName() string { return "pivot_corpus_parents" }
func (pivotCorpusConnectedParent) Connection() string {
	return "connection"
}

// pivotCorpusCustomColumnsParent declares custom timestamp columns — the corpus's parent stub
// overriding getCreatedAtColumn()/getUpdatedAtColumn(). The Go reading resolves the names from the
// parent's declared schema (relations/pivot.go's ReadPivotParent divergence register).
type pivotCorpusCustomColumnsParent struct {
	ID        uint             `gorm:"column:id;primaryKey"`
	CreatedAt *carbon.DateTime `gorm:"autoCreateTime;column:parent_created_at"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:parent_updated_at"`
}

func (pivotCorpusCustomColumnsParent) TableName() string { return "pivot_corpus_parents" }

// pivotCorpusRoleUser is the pivot table's row shape: the delete flip's count assertions read the
// harness rows through it (a bare Where chain compiles no FROM clause; Model scopes the table).
type pivotCorpusRoleUser struct {
	ID      uint64 `gorm:"column:id;primaryKey"`
	Foreign string `gorm:"column:foreign"`
	Other   string `gorm:"column:other"`
	Note    string `gorm:"column:note"`
}

func (pivotCorpusRoleUser) TableName() string { return "role_user" }

// newPivotCorpusBuilder opens the harness database and returns the one builder as both the ORM
// query (schema, seeds, counts) and the relations.Builder the Pivot type executes on:
// newRelationQuery builds the gorm OrmBuilder and returns it under its contractsorm.Query
// interface.
func newPivotCorpusBuilder(t *testing.T) (*databasegorm.OrmBuilder, contractsorm.Query) {
	t.Helper()

	query := newRelationQuery(t)

	builder, ok := query.(*databasegorm.OrmBuilder)
	require.True(t, ok, "the harness query must be the gorm OrmBuilder the pivot executes on")

	return builder, query
}

// --- shared skip reasons ---------------------------------------------------------------------

const (
	skipPivotMutators = `PHP-only assertion (no Go equivalent): the Go ORM has no mutator pipeline — setFooAttribute cannot exist, attributes are stored exactly as written (relations/pivot.go's class mapping) — so the stub's mutatorCalled flag has no counterpart; oracle v12.55.1, executed (/tmp/pivotflips/corpus_probe.php): mutator.fromAttributes.mutatorCalled => true; mutator.fromRawAttributes.mutatorCalled => false`

	skipPivotCasts = `PHP-only assertion (no Go equivalent): the Go ORM has no cast layer (no $casts) — a raw attribute reads back exactly as stored — so the json cast's decode-once pipeline has no counterpart; oracle v12.55.1, executed (/tmp/pivotflips/corpus_probe.php): cast.foo.raw => "{\"name\":\"Taylor\"}"; cast.foo.decodedOnce => {"name":"Taylor"}`
)

// --- ports of the corpus ---------------------------------------------------------------------

func TestPivot_PropertiesAreSetCorrectly(t *testing.T) {
	// Laravel: Pivot::fromAttributes($parent, ['foo' => 'bar', 'created_at' => '2015-09-12'],
	// 'table', true) then asserts getAttributes() (with created_at normalized to
	// '2015-09-12 00:00:00'), the parent's connection name 'connection', table 'table',
	// exists === true, and pivotParent === $parent.
	builder, _ := newPivotCorpusBuilder(t)
	parent := &pivotCorpusConnectedParent{ID: 1}

	pivot, err := relations.FromAttributes(builder, parent, map[string]any{
		"foo":        "bar",
		"created_at": "2015-09-12",
	}, "table", true)
	require.NoError(t, err)

	// Oracle v12.55.1, executed (/tmp/pivotflips/corpus_probe.php): props.attributes.foo => "bar";
	// props.attributes.created_at.stored => "2015-09-12 00:00:00" (the date pipeline normalizes at
	// fill time); props.connection => "connection"; props.table => "table"; props.exists => true;
	// props.pivotParent.same => true. The Go port stores attribute values exactly as written — the
	// missing date pipeline is a registered divergence — so created_at is asserted verbatim; every
	// other facet matches the oracle.
	assert.Equal(t, map[string]any{"foo": "bar", "created_at": "2015-09-12"}, pivot.Attributes())
	assert.Equal(t, "connection", pivot.GetConnectionName())
	assert.Equal(t, "table", pivot.GetTable())
	assert.True(t, pivot.Exists())
	assert.Same(t, parent, pivot.GetPivotParent())
}

func TestPivot_MutatorsAreCalledFromConstructor(t *testing.T) {
	// Laravel: DatabaseEloquentPivotTestMutatorStub::fromAttributes(...) runs setFooAttribute while
	// filling the attributes, so getMutatorCalled() is true.
	t.Skip(skipPivotMutators + "; the corpus asserts the constructor path calls the mutator")
}

func TestPivot_FromRawAttributesDoesNotDoubleMutate(t *testing.T) {
	// Laravel: DatabaseEloquentPivotTestJsonCastStub::fromRawAttributes(...) with a JSON-encoded
	// foo sets the raw value without mutators, and reading $pivot->foo applies the 'json' cast
	// exactly once — the assertion is ['name' => 'Taylor'].
	t.Skip(skipPivotCasts + "; the corpus pins the decode-once behaviour of the json cast pipeline")
}

func TestPivot_FromRawAttributesDoesNotMutate(t *testing.T) {
	// Laravel: fromRawAttributes on the mutator stub never calls setFooAttribute —
	// getMutatorCalled() stays false.
	t.Skip(skipPivotMutators + "; the corpus asserts the raw-attributes path skips the mutator")
}

func TestPivot_PropertiesUnchangedAreNotDirty(t *testing.T) {
	// Laravel: Pivot::fromAttributes syncs the original attributes, so getDirty() is [] while
	// nothing has changed.
	builder, _ := newPivotCorpusBuilder(t)

	pivot, err := relations.FromAttributes(builder, &pivotCorpusParent{ID: 1}, map[string]any{
		"foo":   "bar",
		"shimy": "shake",
	}, "table", true)
	require.NoError(t, err)

	// Oracle v12.55.1, executed: dirty.fresh => [].
	assert.Empty(t, pivot.GetDirty())
}

func TestPivot_PropertiesChangedAreDirty(t *testing.T) {
	// Laravel: assigning $pivot->shimy = 'changed' makes getDirty() === ['shimy' => 'changed'].
	builder, _ := newPivotCorpusBuilder(t)

	pivot, err := relations.FromAttributes(builder, &pivotCorpusParent{ID: 1}, map[string]any{
		"foo":   "bar",
		"shimy": "shake",
	}, "table", true)
	require.NoError(t, err)
	pivot.SetAttribute("shimy", "changed")

	// Oracle v12.55.1, executed: dirty.changed => {"shimy":"changed"} — exactly the assigned key.
	assert.Equal(t, map[string]any{"shimy": "changed"}, pivot.GetDirty())
}

func TestPivot_TimestampPropertyIsSetIfCreatedAtInAttributes(t *testing.T) {
	// Laravel: fromAttributes sets the per-instance $timestamps flag from hasTimestampAttributes():
	// true when the attributes carry the created_at column name, false when they do not. (The
	// corpus's DatabaseEloquentPivotTestDateStub only empties getDates() so PHP's date casting
	// stays out of the assertion; the flag is the observable.)
	builder, _ := newPivotCorpusBuilder(t)

	withCreatedAt, err := relations.FromAttributes(builder, &pivotCorpusParent{ID: 1}, map[string]any{
		"foo":        "bar",
		"created_at": "foo",
	}, "table", false)
	require.NoError(t, err)

	withoutCreatedAt, err := relations.FromAttributes(builder, &pivotCorpusParent{ID: 1}, map[string]any{
		"foo": "bar",
	}, "table", false)
	require.NoError(t, err)

	// Oracle v12.55.1, executed: timestamps.createdAtPresent => true;
	// timestamps.createdAtAbsent => false.
	assert.True(t, withCreatedAt.UsesTimestamps())
	assert.False(t, withoutCreatedAt.UsesTimestamps())
}

func TestPivot_TimestampPropertyIsTrueWhenCreatingFromRawAttributes(t *testing.T) {
	// Laravel: Pivot::fromRawAttributes($parent, ['foo' => 'bar', 'created_at' => 'foo'], 'table')
	// also sets $timestamps true, from the raw attribute keys.
	builder, _ := newPivotCorpusBuilder(t)

	pivot, err := relations.FromRawAttributes(builder, &pivotCorpusParent{ID: 1}, map[string]any{
		"foo":        "bar",
		"created_at": "foo",
	}, "table", false)
	require.NoError(t, err)

	// Oracle v12.55.1, executed: raw.timestamps => true (the raw attribute names are weighed);
	// raw.attributes.created_at => "foo" with raw.originalBag => [] (exists false never syncs),
	// so the instance reads raw.dirty => {"foo":"bar","created_at":"foo"} — the Go port matches
	// all three.
	assert.True(t, pivot.UsesTimestamps())
	assert.Equal(t, map[string]any{"foo": "bar", "created_at": "foo"}, pivot.GetDirty())
}

func TestPivot_KeysCanBeSetProperly(t *testing.T) {
	// Laravel: setPivotKeys('foreign', 'other') then getForeignKey()/getOtherKey() return exactly
	// 'foreign'/'other'.
	builder, _ := newPivotCorpusBuilder(t)

	pivot, err := relations.FromAttributes(builder, &pivotCorpusParent{ID: 1}, map[string]any{"foo": "bar"}, "table", false)
	require.NoError(t, err)
	pivot.SetPivotKeys("foreign", "other")

	// Oracle v12.55.1, executed: keys.foreign => "foreign"; keys.other => "other";
	// keys.related => "other".
	assert.Equal(t, "foreign", pivot.GetForeignKey())
	assert.Equal(t, "other", pivot.GetOtherKey())
}

func TestPivot_DeleteMethodDeletesModelByKeys(t *testing.T) {
	// Laravel: setPivotKeys('foreign', 'other'), foreign/other set to 'foreign.value'/'other.value',
	// then Pivot::delete() -> newQueryWithoutRelationships()->where(['foreign' => 'foreign.value',
	// 'other' => 'other.value'])->delete(), reported as 1. The corpus's assertions are Mockery
	// interactions on a mocked builder, so the flip pins the same observable against the harness
	// sqlite database: delete() removes exactly the row matching BOTH pivot key values and reports
	// the affected count; the neighbours sharing one key each stay.
	builder, query := newPivotCorpusBuilder(t)
	runRelationSchema(t, query,
		`create table role_user (id integer primary key autoincrement, "foreign" text, "other" text, note text)`)

	seed := cloneRelationQuery(t, query)
	for _, row := range []struct{ foreign, other, note string }{
		{"foreign.value", "other.value", "target"},
		{"foreign.value", "other.kept", "shares foreign"},
		{"foreign.kept", "other.value", "shares other"},
	} {
		_, err := seed.Exec(`insert into role_user ("foreign", "other", note) values (?, ?, ?)`, row.foreign, row.other, row.note)
		require.NoError(t, err)
	}

	pivot := relations.NewPivot(builder).SetTable("role_user").SetExists(true)
	pivot.SetPivotKeys("foreign", "other")
	pivot.SetAttribute("foreign", "foreign.value")
	pivot.SetAttribute("other", "other.value")

	rowsAffected, err := pivot.Delete()
	require.NoError(t, err)

	// Oracle v12.55.1, executed (/tmp/pivotflips/delete_probe.php):
	// delete.sql => delete from "role_user" where ("foreign" = ? and "other" = ?)
	//   || ["foreign.value","other.value"]; delete.rowsAffected => 1; delete.existsAfter => false;
	// rows.remaining => the two neighbours (shares foreign / shares other). The oracle deletes an
	// existing pivot (fromAttributes(..., true)), so this instance starts existing as well - the
	// existsAfter clearing is only observable from exists === true.
	assert.EqualValues(t, 1, rowsAffected)
	assert.False(t, pivot.Exists())

	sharesForeign, err := cloneRelationQuery(t, query).Model(&pivotCorpusRoleUser{}).Where("foreign", "foreign.value").Count()
	require.NoError(t, err)
	assert.EqualValues(t, 1, sharesForeign)

	sharesOther, err := cloneRelationQuery(t, query).Model(&pivotCorpusRoleUser{}).Where("other", "other.value").Count()
	require.NoError(t, err)
	assert.EqualValues(t, 1, sharesOther)
}

func TestPivot_PivotModelTableNameIsSingular(t *testing.T) {
	// Laravel: (new Pivot)->getTable() === 'pivot' — the base class derives its table name via
	// Str::snake(Str::singular(class_basename($class))).
	builder, _ := newPivotCorpusBuilder(t)

	// Oracle v12.55.1, executed: table.barePivot => "pivot". The Go fallback derives the same name
	// from the Go type name — the reading of class_basename (relations/pivot.go GetTable).
	assert.Equal(t, "pivot", relations.NewPivot(builder).GetTable())
}

func TestPivot_PivotModelWithParentReturnsParentsTimestampColumns(t *testing.T) {
	// Laravel: a pivot whose pivotParent returns parent_created_at/parent_updated_at exposes those
	// custom column names through getCreatedAtColumn()/getUpdatedAtColumn().
	builder, _ := newPivotCorpusBuilder(t)
	pivot := relations.NewPivot(builder)
	pivot.SetPivotParent(&pivotCorpusCustomColumnsParent{ID: 1})

	// Oracle v12.55.1, executed: columns.withParent.createdAt => "parent_created_at";
	// columns.withParent.updatedAt => "parent_updated_at" — resolved from the parent's declared
	// schema, the Go reading of the parent's getCreatedAtColumn()/getUpdatedAtColumn() overrides.
	assert.Equal(t, "parent_created_at", pivot.GetCreatedAtColumn())
	assert.Equal(t, "parent_updated_at", pivot.GetUpdatedAtColumn())
}

func TestPivot_PivotModelWithoutParentReturnsModelTimestampColumns(t *testing.T) {
	// Laravel: with no parent, the pivot falls back to the base Model's defaults — 'created_at'
	// and 'updated_at' — for its timestamp columns.
	builder, _ := newPivotCorpusBuilder(t)

	// Oracle v12.55.1, executed: columns.bare.createdAt => "created_at";
	// columns.bare.updatedAt => "updated_at".
	pivot := relations.NewPivot(builder)
	assert.Equal(t, "created_at", pivot.GetCreatedAtColumn())
	assert.Equal(t, "updated_at", pivot.GetUpdatedAtColumn())
}

func TestPivot_WithoutRelations(t *testing.T) {
	// Laravel: setRelation('bar', 'baz')/getRelation round-trip; withoutRelations() returns a clone
	// with no relations and no pivotParent while the original keeps both; unsetRelations() clears
	// them on the same instance and returns it. PHP's in-memory relation registry has no Go
	// counterpart (Go models carry declared struct fields; relations/pivot.go registers that
	// WithoutRelations/UnsetRelations clear the pivotParent/pivotRelated links only), so the link
	// facets carry the observable: the copy loses the links and is not the original, the original
	// keeps them, and unset clears and returns the same instance.
	builder, _ := newPivotCorpusBuilder(t)
	original := relations.NewPivot(builder)
	original.SetPivotParent("foo")
	original.SetRelatedModel("baz")

	// Oracle v12.55.1, executed: withoutRelations.originalParent => "foo";
	// withoutRelations.copyParent => null; withoutRelations.sameInstance => false;
	// unsetRelations.parent => null; unsetRelations.sameInstance => true.
	cloned := original.WithoutRelations()
	assert.NotSame(t, original, cloned)
	assert.Equal(t, "foo", original.GetPivotParent())
	assert.Nil(t, cloned.GetPivotParent())
	assert.Equal(t, "baz", original.GetPivotRelated())
	assert.Nil(t, cloned.GetPivotRelated())

	unset := original.UnsetRelations()
	assert.Same(t, original, unset)
	assert.Nil(t, original.GetPivotParent())
	assert.Nil(t, original.GetPivotRelated())
}
