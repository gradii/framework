// Package relations_test drives the Pivot port through a real *gorm.OrmBuilder: the models, the
// pivot and the statements are the production objects, and the only stand-in is a recording
// database/sql driver that answers reads with an empty result set and reports a configurable
// affected-rows count for writes. Every SQL expectation is pinned against the executed oracle
// probes (/tmp/pivotprobes/state_probe.php and /tmp/pivotprobes/sql_probe.php, illuminate/database
// v12.55.1) with the PHP-only facets called out where the shape cannot match byte for byte
// (date normalization, integer inlining in key lists).
package relations_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsdatabase "github.com/gradii/framework/contracts/database"
	contractsorm "github.com/gradii/framework/contracts/database/orm"
	databasedriver "github.com/gradii/framework/database/driver"
	databasegorm "github.com/gradii/framework/database/gorm"
	relations "github.com/gradii/framework/database/gorm/relations"
	querybuilder "github.com/gradii/framework/database/query"
	frameworkerrors "github.com/gradii/framework/errors"
	"github.com/gradii/framework/support/carbon"
)

// --- fixtures --------------------------------------------------------------------------------------

// pivotTestParent is the plain parent model: a key plus the conventional timestamps, no declared
// connection.
type pivotTestParent struct {
	ID        uint             `gorm:"column:id;primaryKey"`
	CreatedAt *carbon.DateTime `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`
}

func (pivotTestParent) TableName() string { return "pivot_parents" }

// pivotTestCustomColumnsParent declares custom timestamp columns — the case
// DatabaseEloquentPivotTest's parent stub covers with getCreatedAtColumn/getUpdatedAtColumn
// overrides.
type pivotTestCustomColumnsParent struct {
	ID        uint             `gorm:"column:id;primaryKey"`
	CreatedAt *carbon.DateTime `gorm:"autoCreateTime;column:parent_created_at"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:parent_updated_at"`
}

func (pivotTestCustomColumnsParent) TableName() string { return "pivot_parents" }

// pivotTestConnectedParent declares a connection name — the value AsPivot::fromAttributes stamps
// onto the pivot (the corpus stubs getConnectionName() with "connection").
type pivotTestConnectedParent struct {
	ID uint `gorm:"column:id;primaryKey"`
}

func (pivotTestConnectedParent) TableName() string { return "pivot_parents" }
func (pivotTestConnectedParent) Connection() string {
	return "connection"
}

// --- recording transport ---------------------------------------------------------------------------

// pivotTestConnector is the base-semantics transport with a configurable affected-rows count, so
// the pivot's write results can be pinned (the shared sqlConnector reports zero).
type pivotTestConnector struct {
	recorder *statementRecorder
	affected int64
}

func (c pivotTestConnector) Connect(context.Context) (driver.Conn, error) {
	return &pivotTestConn{recorder: c.recorder, affected: c.affected}, nil
}

func (c pivotTestConnector) Driver() driver.Driver { return pivotTestDriver(c) }

type pivotTestDriver struct {
	recorder *statementRecorder
	affected int64
}

func (d pivotTestDriver) Open(string) (driver.Conn, error) {
	return &pivotTestConn{recorder: d.recorder, affected: d.affected}, nil
}

type pivotTestConn struct {
	recorder *statementRecorder
	affected int64
}

func (c *pivotTestConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }

func (c *pivotTestConn) Close() error { return nil }

func (c *pivotTestConn) Begin() (driver.Tx, error) {
	return nil, errors.New("the pivot recording driver does not support transactions")
}

func (c *pivotTestConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	c.recorder.record(query, args)

	return pivotTestResult{affected: c.affected}, nil
}

// pivotTestResult answers both halves of driver.Result: driver.RowsAffected alone reports
// LastInsertId as unsupported, which the ORM's single-row insert path asks for.
type pivotTestResult struct {
	affected int64
}

func (r pivotTestResult) LastInsertId() (int64, error) { return 0, nil }

func (r pivotTestResult) RowsAffected() (int64, error) { return r.affected, nil }

func (c *pivotTestConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.recorder.record(query, args)

	return emptyRows{}, nil
}

func (c *pivotTestConn) Ping(context.Context) error { return nil }

// newPivotTestBuilder builds a real *gorm.OrmBuilder on the pivot recording transport.
func newPivotTestBuilder(t *testing.T, affected int64) (*databasegorm.OrmBuilder, *statementRecorder) {
	t.Helper()

	recorder := &statementRecorder{}
	db := sql.OpenDB(pivotTestConnector{recorder: recorder, affected: affected})

	grammar := querybuilder.NewBaseGrammar("")
	components := databasedriver.QueryComponents{Grammar: grammar, Processor: querybuilder.NewProcessor("")}

	builder := databasegorm.NewOrmBuilder(
		context.Background(),
		nil,
		contractsdatabase.Config{Driver: "sqlite"},
		db,
		nil,
		nil,
		nil,
		nil,
		&databasegorm.Conditions{},
		nil,
		components,
	)
	t.Cleanup(func() { _ = db.Close() })

	return builder, recorder
}

// newPivot builds the pivot the way AsPivot::fromAttributes does over the given parent.
func newPivot(t *testing.T, builder *databasegorm.OrmBuilder, parent any, attributes map[string]any, exists bool) *relations.Pivot {
	t.Helper()

	pivot, err := relations.FromAttributes(builder, parent, attributes, "role_user", exists)
	require.NoError(t, err)

	return pivot
}

// --- observer --------------------------------------------------------------------------------------

// pivotTestObserver records the pivot events the frozen dispatch delivers. The four mandatory
// Observer methods are no-ops beyond the recording; the optional interface family supplies the
// rest of the model-event vocabulary.
type pivotTestObserver struct {
	events []string
}

func (o *pivotTestObserver) record(name string) error {
	o.events = append(o.events, name)

	return nil
}

func (o *pivotTestObserver) Created(contractsorm.Event) error      { return o.record("created") }
func (o *pivotTestObserver) Updated(contractsorm.Event) error      { return o.record("updated") }
func (o *pivotTestObserver) Deleted(contractsorm.Event) error      { return o.record("deleted") }
func (o *pivotTestObserver) ForceDeleted(contractsorm.Event) error { return o.record("force_deleted") }
func (o *pivotTestObserver) Creating(contractsorm.Event) error     { return o.record("creating") }
func (o *pivotTestObserver) Saving(contractsorm.Event) error       { return o.record("saving") }
func (o *pivotTestObserver) Saved(contractsorm.Event) error        { return o.record("saved") }
func (o *pivotTestObserver) Updating(contractsorm.Event) error     { return o.record("updating") }
func (o *pivotTestObserver) Deleting(contractsorm.Event) error     { return o.record("deleting") }

// --- FromAttributes / FromRawAttributes ------------------------------------------------------------

func TestPivot_FromAttributesState(t *testing.T) {
	builder, _ := newPivotTestBuilder(t, 1)
	parent := &pivotTestParent{ID: 1}

	pivot := newPivot(t, builder, parent, map[string]any{"foo": "bar", "created_at": "2015-09-12"}, true)

	// Oracle v12.55.1, executed: fromAttributes(..., ['foo' => 'bar', 'created_at' => '2015-09-12'],
	// 'table', true) reports the attributes (with created_at normalized to a Carbon dumped
	// "2015-09-12 00:00:00"), connection "connection", table "table", exists true, the same parent
	// and timestamps true. The Go port stores the attribute exactly as written — there is no date
	// pipeline — which is the one facet of that comparison that cannot match.
	assert.Equal(t, "bar", pivot.GetAttribute("foo"))
	assert.Equal(t, "2015-09-12", pivot.GetAttribute("created_at"))
	assert.Equal(t, "role_user", pivot.GetTable())
	assert.True(t, pivot.Exists())
	assert.Same(t, parent, pivot.GetPivotParent())
	assert.True(t, pivot.UsesTimestamps())
	assert.Empty(t, pivot.GetDirty())
	assert.Equal(t, "id", pivot.GetKeyName())

	// Without created_at the flag is false (oracle: fromAttributes ['foo' => 'bar'] -> timestamps
	// false).
	flagless := newPivot(t, builder, parent, map[string]any{"foo": "bar"}, false)
	assert.False(t, flagless.UsesTimestamps())
	assert.False(t, flagless.Exists())

	// A parent declaring a connection name is stamped through (oracle: getConnectionName
	// "connection").
	connected := newPivot(t, builder, &pivotTestConnectedParent{ID: 1}, map[string]any{"foo": "bar"}, true)
	assert.Equal(t, "connection", connected.GetConnectionName())
}

func TestPivot_FromAttributesCopiesTheBag(t *testing.T) {
	builder, _ := newPivotTestBuilder(t, 1)
	attributes := map[string]any{"foo": "bar"}

	pivot := newPivot(t, builder, &pivotTestParent{ID: 1}, attributes, true)

	// PHP arrays pass by value: mutating the caller's array after the call does not reach the
	// instance, and the Attributes() copy does not share the live bag.
	attributes["foo"] = "changed"
	assert.Equal(t, "bar", pivot.GetAttribute("foo"))

	exposed := pivot.Attributes()
	exposed["foo"] = "mutated"
	assert.Equal(t, "bar", pivot.GetAttribute("foo"))
}

func TestPivot_FromRawAttributesState(t *testing.T) {
	builder, _ := newPivotTestBuilder(t, 1)
	parent := &pivotTestParent{ID: 1}

	// Oracle v12.55.1, executed: fromRawAttributes(..., ['foo' => 'bar', 'created_at' => 'foo'],
	// 'table') reports timestamps true (the parent-delegated created_at name is present in the raw
	// attributes), exists false and dirty {foo, created_at} (the raw bag never synced).
	raw, err := relations.FromRawAttributes(builder, parent, map[string]any{"foo": "bar", "created_at": "foo"}, "role_user", false)
	require.NoError(t, err)
	assert.Equal(t, "foo", raw.GetAttribute("created_at"), "raw values are kept verbatim")
	assert.True(t, raw.UsesTimestamps())
	assert.False(t, raw.Exists())
	assert.Equal(t, map[string]any{"foo": "bar", "created_at": "foo"}, raw.GetDirty())

	// Oracle: the same call with exists true reports dirty [] — the raw bag syncs.
	synced, err := relations.FromRawAttributes(builder, parent, map[string]any{"foo": "bar", "created_at": "foo"}, "role_user", true)
	require.NoError(t, err)
	assert.Empty(t, synced.GetDirty())
	assert.True(t, synced.Exists())

	// Oracle: a custom-columns parent makes the flag follow parent_created_at.
	custom, err := relations.FromRawAttributes(builder, &pivotTestCustomColumnsParent{ID: 1}, map[string]any{"parent_created_at": "x"}, "role_user", false)
	require.NoError(t, err)
	assert.True(t, custom.UsesTimestamps())

	// And a raw bag without any timestamp name stays flagless.
	flagless, err := relations.FromRawAttributes(builder, parent, map[string]any{"foo": "bar"}, "role_user", false)
	require.NoError(t, err)
	assert.False(t, flagless.UsesTimestamps())
}

func TestPivot_DirtyTracking(t *testing.T) {
	builder, _ := newPivotTestBuilder(t, 1)
	parent := &pivotTestParent{ID: 1}

	// Oracle v12.55.1, executed: a freshly built pivot reads dirty []; one assignment
	// ($pivot->shimy = 'changed') reads exactly {'shimy' => 'changed'}; syncOriginal clears it.
	pivot := newPivot(t, builder, parent, map[string]any{"foo": "bar", "shimy": "shake"}, true)
	assert.Empty(t, pivot.GetDirty())

	pivot.SetAttribute("shimy", "changed")
	assert.Equal(t, map[string]any{"shimy": "changed"}, pivot.GetDirty())
	assert.True(t, pivot.IsDirty())
	assert.True(t, pivot.IsDirty("shimy"))
	assert.False(t, pivot.IsDirty("foo"))

	pivot.SyncOriginal()
	assert.Empty(t, pivot.GetDirty())
	assert.Equal(t, "changed", pivot.GetAttribute("shimy"))

	// Oracle: 1 vs "1" stays clean — originalIsEquivalent's numeric strcmp branch.
	numeric := newPivot(t, builder, parent, map[string]any{"n": 1}, true)
	numeric.SetAttribute("n", "1")
	assert.Empty(t, numeric.GetDirty())
}

func TestPivot_Keys(t *testing.T) {
	builder, _ := newPivotTestBuilder(t, 1)
	pivot := newPivot(t, builder, &pivotTestParent{ID: 1}, map[string]any{"foo": "bar"}, true)

	// Oracle v12.55.1, executed: setPivotKeys('foreign', 'other') -> getForeignKey() "foreign",
	// getOtherKey() "other" (getRelatedKey() the same).
	pivot.SetPivotKeys("foreign", "other")
	assert.Equal(t, "foreign", pivot.GetForeignKey())
	assert.Equal(t, "other", pivot.GetOtherKey())
	assert.Equal(t, "other", pivot.GetRelatedKey())

	// The PivotKeyed contract reports the pair in the order SetKeysForSelectQuery writes it.
	var keyed contractsorm.PivotKeyed = pivot
	assert.Equal(t, []string{"foreign", "other"}, keyed.PivotKeys())
}

func TestPivot_ColumnDelegation(t *testing.T) {
	builder, _ := newPivotTestBuilder(t, 1)

	// Oracle v12.55.1, executed: a pivot with a custom-columns parent reports
	// parent_created_at / parent_updated_at; without a parent it reports the Model defaults
	// created_at / updated_at, and `(new Pivot)->getTable()` reports "pivot".
	withParent := newPivot(t, builder, &pivotTestCustomColumnsParent{ID: 1}, map[string]any{}, true)
	assert.Equal(t, "parent_created_at", withParent.GetCreatedAtColumn())
	assert.Equal(t, "parent_updated_at", withParent.GetUpdatedAtColumn())

	bare := relations.NewPivot(builder)
	assert.Equal(t, "created_at", bare.GetCreatedAtColumn())
	assert.Equal(t, "updated_at", bare.GetUpdatedAtColumn())
	assert.Equal(t, "pivot", bare.GetTable())

	bare.SetTable("role_user")
	assert.Equal(t, "role_user", bare.GetTable())
}

func TestPivot_QueueableID(t *testing.T) {
	builder, _ := newPivotTestBuilder(t, 1)
	parent := &pivotTestParent{ID: 1}

	// Oracle v12.55.1, executed: {foreign, other} -> "foreign:foreign.value:other:other.value";
	// an own id 7 -> 7; null values -> "foreign::other:"; a null id falls back to the segmented
	// form -> "foreign:f:other:".
	pivot := newPivot(t, builder, parent, map[string]any{"foreign": "foreign.value", "other": "other.value"}, true)
	pivot.SetPivotKeys("foreign", "other")
	assert.Equal(t, "foreign:foreign.value:other:other.value", pivot.GetQueueableID())

	keyed := newPivot(t, builder, parent, map[string]any{"id": 7, "foreign": "f"}, true)
	keyed.SetPivotKeys("foreign", "other")
	assert.EqualValues(t, 7, keyed.GetQueueableID())

	nulls := newPivot(t, builder, parent, map[string]any{"foreign": nil, "other": nil}, true)
	nulls.SetPivotKeys("foreign", "other")
	assert.Equal(t, "foreign::other:", nulls.GetQueueableID())

	nullKey := newPivot(t, builder, parent, map[string]any{"id": nil, "foreign": "f"}, true)
	nullKey.SetPivotKeys("foreign", "other")
	assert.Equal(t, "foreign:f:other:", nullKey.GetQueueableID())
}

// --- Save ------------------------------------------------------------------------------------------

func TestPivot_SaveExistingWithoutKey(t *testing.T) {
	builder, recorder := newPivotTestBuilder(t, 1)

	pivot := newPivot(t, builder, &pivotTestParent{ID: 1}, map[string]any{"foreign": "fv", "other": "ov"}, true)
	pivot.SetPivotKeys("foreign", "other")
	pivot.SetAttribute("note", "changed")
	require.NoError(t, pivot.Save())

	statements := recorder.take()
	require.Len(t, statements, 1)
	// Oracle v12.55.1, executed (sqlite): `update "role_user" set "note" = ? where "foreign" = ?
	// and "other" = ?` with bindings ["changed","fv","ov"] — the key pair read from the original
	// bag, the update through setKeysForSaveQuery.
	assert.Equal(t, `update "role_user" set "note" = ? where "foreign" = ? and "other" = ?`, statements[0].query)
	require.Len(t, statements[0].args, 3)
	assert.EqualValues(t, "changed", statements[0].args[0])
	assert.EqualValues(t, "fv", statements[0].args[1])
	assert.EqualValues(t, "ov", statements[0].args[2])

	assert.True(t, pivot.Exists(), "an existing pivot stays existing across a save")
	assert.Empty(t, pivot.GetDirty(), "finishSave syncs the original bag")
}

func TestPivot_SaveSkipsTheStatementWhenClean(t *testing.T) {
	builder, recorder := newPivotTestBuilder(t, 1)

	pivot := newPivot(t, builder, &pivotTestParent{ID: 1}, map[string]any{"foreign": "fv", "other": "ov"}, true)
	pivot.SetPivotKeys("foreign", "other")
	require.NoError(t, pivot.Save())

	// Oracle v12.55.1, executed: an untouched pivot's save returns true without a statement
	// (isDirty() is false, so performUpdate never runs).
	assert.Empty(t, recorder.take())
}

func TestPivot_CleanSaveFiresOnlySaveEvents(t *testing.T) {
	builder, recorder := newPivotTestBuilder(t, 1)
	observer := &pivotTestObserver{}
	builder.Observe(&relations.Pivot{}, observer)

	// Oracle v12.55.1, executed (verifier probe /tmp/morphverify/p1_saveflow.php, cases A/B/E):
	// Model::save runs performUpdate only when the model is dirty, so a clean timestamped pivot
	// fires no updating / updated events, stamps no updated_at and emits no statement; the
	// saved event and the original-bag sync still happen (finishSave).
	pivot := newPivot(t, builder, &pivotTestParent{ID: 1}, map[string]any{"foreign": "fv", "other": "ov", "created_at": "2015-09-12 00:00:00"}, true)
	pivot.SetPivotKeys("foreign", "other")
	require.NoError(t, pivot.Save())

	assert.Empty(t, recorder.take())
	assert.Equal(t, []string{"saving", "saved"}, observer.events)
	assert.Nil(t, pivot.GetAttribute("updated_at"), "a clean save never stamps updated_at")
}

func TestPivot_SaveNewEmptyAttributesIsANoOp(t *testing.T) {
	builder, recorder := newPivotTestBuilder(t, 1)
	observer := &pivotTestObserver{}
	builder.Observe(&relations.Pivot{}, observer)

	// Oracle v12.55.1, executed (verifier probe /tmp/morphverify/p8_empty_save.php):
	// performInsert's empty-attribute early return — no statement, no created event, exists
	// stays false; finishSave still fires saved and syncs the original bag.
	pivot := newPivot(t, builder, &pivotTestParent{ID: 1}, map[string]any{}, false)
	pivot.SetPivotKeys("foreign", "other")
	require.NoError(t, pivot.Save())

	assert.Empty(t, recorder.take())
	assert.Equal(t, []string{"saving", "creating", "saved"}, observer.events)
	assert.False(t, pivot.Exists(), "performInsert returns before exists is set")
}

func TestPivot_SaveStampsUpdatedAtWhenTimestampsFlagOn(t *testing.T) {
	builder, recorder := newPivotTestBuilder(t, 1)

	pivot := newPivot(t, builder, &pivotTestParent{ID: 1}, map[string]any{"foreign": "fv", "other": "ov", "created_at": "2015-09-12 00:00:00"}, true)
	pivot.SetPivotKeys("foreign", "other")
	pivot.SetAttribute("note", "n2")
	require.NoError(t, pivot.Save())

	statements := recorder.take()
	require.Len(t, statements, 1)
	// Oracle v12.55.1, executed: `update "role_user" set "note" = ?, "updated_at" = ? where
	// "foreign" = ? and "other" = ?` — an existing pivot stamps only updated_at (created_at is
	// synced, not re-stamped).
	assert.Equal(t, `update "role_user" set "note" = ?, "updated_at" = ? where "foreign" = ? and "other" = ?`, statements[0].query)
	require.Len(t, statements[0].args, 4)
	assert.EqualValues(t, "n2", statements[0].args[0])
	stamp, ok := statements[0].args[1].(time.Time)
	require.True(t, ok, "expected a timestamp binding, got %T", statements[0].args[1])
	assert.WithinDuration(t, time.Now(), stamp, time.Minute)
	assert.EqualValues(t, "fv", statements[0].args[2])
	assert.EqualValues(t, "ov", statements[0].args[3])

	assert.False(t, pivot.IsDirty("created_at"), "created_at is never dirtied on the update path")
}

func TestPivot_SaveOwnKey(t *testing.T) {
	builder, recorder := newPivotTestBuilder(t, 1)

	pivot := newPivot(t, builder, &pivotTestParent{ID: 1}, map[string]any{"id": uint64(7), "foreign": "f7"}, true)
	pivot.SetPivotKeys("foreign", "other")
	pivot.SetAttribute("note", "n3")
	require.NoError(t, pivot.Save())

	statements := recorder.take()
	require.Len(t, statements, 1)
	// Oracle v12.55.1, executed: `update "role_user" set "note" = ? where "id" = ?` with [n3, 7] —
	// the own key takes setKeysForSaveQuery's primary-key branch.
	assert.Equal(t, `update "role_user" set "note" = ? where "id" = ?`, statements[0].query)
	require.Len(t, statements[0].args, 2)
	assert.EqualValues(t, "n3", statements[0].args[0])
	assert.EqualValues(t, 7, statements[0].args[1])
}

func TestPivot_SaveOwnKeyChangedKeepsOriginalWhere(t *testing.T) {
	builder, recorder := newPivotTestBuilder(t, 1)

	pivot := newPivot(t, builder, &pivotTestParent{ID: 1}, map[string]any{"id": uint64(7)}, true)
	pivot.SetPivotKeys("foreign", "other")
	pivot.SetAttribute("note", "n4")
	pivot.SetAttribute("id", uint64(8))
	require.NoError(t, pivot.Save())

	statements := recorder.take()
	require.Len(t, statements, 1)
	// Oracle v12.55.1, executed: `update "role_user" set "id" = ?, "note" = ? where "id" = ?` with
	// [8, n4, 7] — getKeyForSaveQuery reads original[id] before the current attribute.
	assert.Equal(t, `update "role_user" set "id" = ?, "note" = ? where "id" = ?`, statements[0].query)
	require.Len(t, statements[0].args, 3)
	assert.EqualValues(t, 8, statements[0].args[0])
	assert.EqualValues(t, "n4", statements[0].args[1])
	assert.EqualValues(t, 7, statements[0].args[2])
}

func TestPivot_SaveForeignKeyChangeKeepsOriginalWhere(t *testing.T) {
	builder, recorder := newPivotTestBuilder(t, 1)

	pivot := newPivot(t, builder, &pivotTestParent{ID: 1}, map[string]any{"foreign": "fv", "other": "ov"}, true)
	pivot.SetPivotKeys("foreign", "other")
	pivot.SetAttribute("note", "n5")
	pivot.SetAttribute("foreign", "CHANGED")
	require.NoError(t, pivot.Save())

	statements := recorder.take()
	require.Len(t, statements, 1)
	// Oracle v12.55.1, executed: `update "role_user" set "foreign" = ?, "note" = ? where "foreign"
	// = ? and "other" = ?` with [CHANGED, n5, fv, ov] — the changed value goes into the update,
	// the original key pair stays in the where.
	assert.Equal(t, `update "role_user" set "foreign" = ?, "note" = ? where "foreign" = ? and "other" = ?`, statements[0].query)
	require.Len(t, statements[0].args, 4)
	assert.EqualValues(t, "CHANGED", statements[0].args[0])
	assert.EqualValues(t, "n5", statements[0].args[1])
	assert.EqualValues(t, "fv", statements[0].args[2])
	assert.EqualValues(t, "ov", statements[0].args[3])
}

func TestPivot_SaveNewPivotInserts(t *testing.T) {
	builder, recorder := newPivotTestBuilder(t, 1)

	pivot := newPivot(t, builder, &pivotTestParent{ID: 1}, map[string]any{"foreign": "nf", "other": "no", "created_at": "2015-09-12 00:00:00"}, false)
	pivot.SetPivotKeys("foreign", "other")
	require.NoError(t, pivot.Save())

	statements := recorder.take()
	require.Len(t, statements, 1)
	// Oracle v12.55.1, executed: `insert into "role_user" ("foreign", "other", "created_at",
	// "updated_at") values (?, ?, ?, ?)` re-stamping BOTH timestamps (updateTimestamps runs for a
	// not-existing pivot and the provided created_at is clean, so now wins). The Go insert renders
	// its columns in the framework's sorted-key order — the equivalent statement with the bindings
	// matched by column name.
	assert.Equal(t, `insert into "role_user" ("created_at", "foreign", "other", "updated_at") values (?, ?, ?, ?)`, statements[0].query)
	require.Len(t, statements[0].args, 4)
	created, ok := statements[0].args[0].(time.Time)
	require.True(t, ok, "expected the created_at stamp, got %T", statements[0].args[0])
	assert.WithinDuration(t, time.Now(), created, time.Minute)
	assert.EqualValues(t, "nf", statements[0].args[1])
	assert.EqualValues(t, "no", statements[0].args[2])
	updated, ok := statements[0].args[3].(time.Time)
	require.True(t, ok, "expected the updated_at stamp, got %T", statements[0].args[3])
	assert.WithinDuration(t, time.Now(), updated, time.Minute)

	assert.True(t, pivot.Exists())
	assert.Empty(t, pivot.GetDirty())
	assert.Nil(t, pivot.GetAttribute("id"), "Pivot $incrementing false: the insert reads back no key")
}

// --- Delete ----------------------------------------------------------------------------------------

func TestPivot_DeleteWithoutKey(t *testing.T) {
	builder, recorder := newPivotTestBuilder(t, 1)

	pivot := newPivot(t, builder, &pivotTestParent{ID: 1}, map[string]any{"foreign": "fv", "other": "ov"}, true)
	pivot.SetPivotKeys("foreign", "other")

	rows, err := pivot.Delete()
	require.NoError(t, err)
	assert.EqualValues(t, 1, rows)

	statements := recorder.take()
	require.Len(t, statements, 1)
	// Oracle v12.55.1, executed: `delete from "role_user" where ("foreign" = ? and "other" = ?)` —
	// the where([...]) map form nests the pair; the affected rows are reported.
	assert.Equal(t, `delete from "role_user" where ("foreign" = ? and "other" = ?)`, statements[0].query)
	require.Len(t, statements[0].args, 2)
	assert.EqualValues(t, "fv", statements[0].args[0])
	assert.EqualValues(t, "ov", statements[0].args[1])

	assert.False(t, pivot.Exists(), "the no-key delete flips exists after the statement")
}

func TestPivot_DeleteOwnKey(t *testing.T) {
	builder, recorder := newPivotTestBuilder(t, 1)

	pivot := newPivot(t, builder, &pivotTestParent{ID: 1}, map[string]any{"id": uint64(9)}, true)
	pivot.SetPivotKeys("foreign", "other")

	rows, err := pivot.Delete()
	require.NoError(t, err)
	assert.EqualValues(t, 1, rows)

	statements := recorder.take()
	require.Len(t, statements, 1)
	// Oracle v12.55.1, executed: `delete from "role_user" where "id" = ?` with [7] — the Model
	// delete path through setKeysForSaveQuery.
	assert.Equal(t, `delete from "role_user" where "id" = ?`, statements[0].query)
	require.Len(t, statements[0].args, 1)
	assert.EqualValues(t, 9, statements[0].args[0])
	assert.False(t, pivot.Exists())
}

func TestPivot_DeleteNotExistingOwnKeyEmitsNothing(t *testing.T) {
	builder, recorder := newPivotTestBuilder(t, 1)

	pivot := newPivot(t, builder, &pivotTestParent{ID: 1}, map[string]any{"id": uint64(9)}, false)
	pivot.SetPivotKeys("foreign", "other")

	// Oracle v12.55.1, executed: Model::delete returns null before any event or statement when
	// the model does not exist.
	rows, err := pivot.Delete()
	require.NoError(t, err)
	assert.EqualValues(t, 0, rows)
	assert.Empty(t, recorder.take())
}

func TestPivot_DeleteWithoutKeyNotExistingStillRunsTheStatement(t *testing.T) {
	builder, recorder := newPivotTestBuilder(t, 1)

	pivot := newPivot(t, builder, &pivotTestParent{ID: 1}, map[string]any{"foreign": "nope", "other": "nope"}, false)
	pivot.SetPivotKeys("foreign", "other")

	// Oracle v12.55.1, executed: the AsPivot no-key delete has NO exists check — the statement
	// runs for a never-existing pivot and exists turns false either way.
	rows, err := pivot.Delete()
	require.NoError(t, err)
	assert.EqualValues(t, 1, rows)

	statements := recorder.take()
	require.Len(t, statements, 1)
	assert.Equal(t, `delete from "role_user" where ("foreign" = ? and "other" = ?)`, statements[0].query)
	assert.False(t, pivot.Exists())
}

// --- events ----------------------------------------------------------------------------------------

func TestPivot_Events(t *testing.T) {
	builder, _ := newPivotTestBuilder(t, 1)
	observer := &pivotTestObserver{}
	builder.Observe(&relations.Pivot{}, observer)

	parent := &pivotTestParent{ID: 1}

	// Model::save: saving -> updating -> updated -> saved on the update path.
	pivot := newPivot(t, builder, parent, map[string]any{"foreign": "fv", "other": "ov"}, true)
	pivot.SetPivotKeys("foreign", "other")
	pivot.SetAttribute("note", "changed")
	require.NoError(t, pivot.Save())
	assert.Equal(t, []string{"saving", "updating", "updated", "saved"}, observer.events)

	// Model::save: saving -> creating -> created -> saved on the insert path.
	observer.events = nil
	fresh := newPivot(t, builder, parent, map[string]any{"foreign": "nf"}, false)
	fresh.SetPivotKeys("foreign", "other")
	require.NoError(t, fresh.Save())
	assert.Equal(t, []string{"saving", "creating", "created", "saved"}, observer.events)

	// AsPivot::delete: deleting -> deleted.
	observer.events = nil
	require.NoError(t, func() error { _, err := pivot.Delete(); return err }())
	assert.Equal(t, []string{"deleting", "deleted"}, observer.events)
}

// --- restoration -----------------------------------------------------------------------------------

func TestPivot_Restoration(t *testing.T) {
	builder, recorder := newPivotTestBuilder(t, 1)
	parent := &pivotTestParent{ID: 1}

	base := newPivot(t, builder, parent, map[string]any{}, true)
	base.SetPivotKeys("foreign", "other")

	var dest []map[string]any

	// Oracle v12.55.1, executed: "foreign:f1:other:o1" ->
	// `select * from "table" where "foreign" = ? and "other" = ?`.
	query, err := base.NewQueryForRestoration("foreign:f1:other:o1")
	require.NoError(t, err)
	require.NoError(t, query.Get(&dest))
	statements := recorder.take()
	require.Len(t, statements, 1)
	assert.Equal(t, `select * from "role_user" where "foreign" = ? and "other" = ?`, statements[0].query)
	require.Len(t, statements[0].args, 2)
	assert.EqualValues(t, "f1", statements[0].args[0])
	assert.EqualValues(t, "o1", statements[0].args[1])

	// Oracle: a list of segmented ids ->
	// `select * from "table" where ("foreign" = ? and "other" = ?) or ("foreign" = ? and "other" = ?)`.
	query, err = base.NewQueryForRestoration([]any{"foreign:f1:other:o1", "foreign:f2:other:o2"})
	require.NoError(t, err)
	require.NoError(t, query.Get(&dest))
	statements = recorder.take()
	require.Len(t, statements, 1)
	assert.Equal(t, `select * from "role_user" where ("foreign" = ? and "other" = ?) or ("foreign" = ? and "other" = ?)`, statements[0].query)
	require.Len(t, statements[0].args, 4)
	assert.EqualValues(t, "f1", statements[0].args[0])
	assert.EqualValues(t, "o1", statements[0].args[1])
	assert.EqualValues(t, "f2", statements[0].args[2])
	assert.EqualValues(t, "o2", statements[0].args[3])

	// Oracle: the scalar 7 -> `select * from "table" where "table"."id" = ?` — Model
	// newQueryForRestoration's whereKey over the qualified pivot key.
	query, err = base.NewQueryForRestoration(7)
	require.NoError(t, err)
	require.NoError(t, query.Get(&dest))
	statements = recorder.take()
	require.Len(t, statements, 1)
	assert.Equal(t, `select * from "role_user" where "role_user"."id" = ?`, statements[0].query)
	require.Len(t, statements[0].args, 1)
	assert.EqualValues(t, 7, statements[0].args[0])

	// Oracle: the list [7, 8] -> `... where "table"."id" in (7, 8)` (Laravel inlines integer key
	// lists); the Go port binds them through the framework's WhereIn — `in (?, ?)`.
	query, err = base.NewQueryForRestoration([]any{7, 8})
	require.NoError(t, err)
	require.NoError(t, query.Get(&dest))
	statements = recorder.take()
	require.Len(t, statements, 1)
	assert.Equal(t, `select * from "role_user" where "role_user"."id" in (?, ?)`, statements[0].query)
	require.Len(t, statements[0].args, 2)
	assert.EqualValues(t, 7, statements[0].args[0])
	assert.EqualValues(t, 8, statements[0].args[1])
}

func TestPivot_RestorationInvalidID(t *testing.T) {
	builder, _ := newPivotTestBuilder(t, 1)
	base := newPivot(t, builder, &pivotTestParent{ID: 1}, map[string]any{}, true)
	base.SetPivotKeys("foreign", "other")

	// A segmented id that cannot name the key pair fails loudly — PHP errors on the same input
	// with an undefined offset.
	_, err := base.NewQueryForRestoration("foreign:f1")
	assert.ErrorIs(t, err, frameworkerrors.OrmPivotRestorationIDInvalid)

	_, err = base.NewQueryForRestoration([]any{"foreign:f1:other:o1", "foreign:f2"})
	assert.ErrorIs(t, err, frameworkerrors.OrmPivotRestorationIDInvalid)
}

// --- relations links -------------------------------------------------------------------------------

func TestPivot_WithoutRelationsAndUnsetRelations(t *testing.T) {
	builder, _ := newPivotTestBuilder(t, 1)
	parent := &pivotTestParent{ID: 1}

	pivot := newPivot(t, builder, parent, map[string]any{"foreign": "fv", "other": "ov"}, true)
	pivot.SetRelatedModel("related")

	// Oracle v12.55.1, executed: withoutRelations() returns a different instance with the parent
	// cleared while the original keeps it (and the related link, the Go stand-in for the registry
	// this package cannot carry).
	copied := pivot.WithoutRelations()
	assert.NotSame(t, pivot, copied)
	assert.Nil(t, copied.GetPivotParent())
	assert.Nil(t, copied.GetPivotRelated())
	assert.Same(t, parent, pivot.GetPivotParent())
	assert.Equal(t, "related", pivot.GetPivotRelated())

	// The copy owns its bags: writing to the copy leaves the original untouched.
	copied.SetAttribute("foreign", "other-value")
	assert.Equal(t, "fv", pivot.GetAttribute("foreign"))

	// Oracle: unsetRelations() clears the links on the instance itself and returns it.
	unset := pivot.UnsetRelations()
	assert.Same(t, pivot, unset)
	assert.Nil(t, pivot.GetPivotParent())
	assert.Nil(t, pivot.GetPivotRelated())
}

// --- named connections -----------------------------------------------------------------------------

func TestPivot_NamedConnectionWithoutConfig(t *testing.T) {
	builder, recorder := newPivotTestBuilder(t, 1)

	// The pivot's declared connection is read at construction; resolving it without a configured
	// connection reports the framework's named config error instead of panicking.
	pivot := newPivot(t, builder, &pivotTestConnectedParent{ID: 1}, map[string]any{"foreign": "fv", "other": "ov"}, true)
	pivot.SetPivotKeys("foreign", "other")
	assert.Equal(t, "connection", pivot.GetConnectionName())

	_, err := pivot.Delete()
	assert.ErrorIs(t, err, frameworkerrors.DatabaseConfigNotFound)
	assert.Empty(t, recorder.take())
}
