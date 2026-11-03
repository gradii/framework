package morph_to

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/database/orm/morphmap"
)

// This file ports Laravel's tests/Database/DatabaseEloquentMorphToTest.php (12.x) onto the
// framework ORM: one Go test per Laravel test method, named TestMorphTo_<method without the test
// prefix>. The corpus is mock-built end to end — a mocked Builder and mocked Models behind a real
// MorphTo — so its relation-object internals (the lookup dictionary, the morphTo() construction
// branches) have no Go counterpart and stay skipped with the concrete reason, while the
// is()/isNot() comparison now runs against the relation object below. The two tests that exercise
// real morph writing — associate() and dissociate() — are ported against the harness sqlite
// database, seeded and read through the ORM.
//
// The port keeps the corpus's columns: the MorphTo under test is declared the way getRelation()
// constructs it — new MorphTo($builder, $parent, 'foreign_key', 'id', 'morph_type', 'relation') —
// so the relation maps to MorphTo{Name: "relation", TypeColumn: "morph_type", IDColumn:
// "foreign_key"}. The stub tables are the ones the corpus's stubs name:
// eloquent_morph_to_model_stubs (id, foreign_key, morph_type) and eloquent_morph_to_related_stubs
// (id only).
//
// Every behavior the ported tests assert was measured against the harness database before the
// tests were written (probe): Associate() sets the parent's foreign key to the associate's key
// and the morph type column to its morph class, persists the parent, and a fresh Load then
// resolves the association; Dissociate() clears both columns to their Go zero values, persists,
// and a fresh Load resolves to nil. Laravel's associate(null) wears the same attribute effect —
// the foreign key and the morph type are written null — and the port routes it through the same
// clear-and-persist write, so the test below asserts it for real. The four withDefault tests assert the default surface with real rows; the merge
// semantics were pinned by executing the corpus's scenarios through the oracle
// (.claude/laravel-oracle/oracle.php against illuminate/database v12.55.1, in-memory sqlite): a
// missing target loads a fresh instance of the class the type column names, an empty type column
// yields the parent's own class (newRelatedInstanceFor re-invokes $parent->relation(); the
// null-type branch is morphEagerTo, whose related model is the parent), the dynamic / array forms
// fill the instance, and a matched row wins. No SQL string is asserted in this file (the corpus's
// builder expectations are mock interactions with no call counts), so no oracle SQL pins appear.

// --- models ------------------------------------------------------------------

// morphToModelStub ports EloquentMorphToModelStub: the corpus's parent table, whose real columns
// are the foreign key plus the morph type column getRelation() hands the MorphTo constructor
// ('morph_type'; the stub's own morphTo() would guess relation_type, but every relation under test
// is the constructed one). Its relation destination is an `any` field — the shape the loader fills
// with the concrete type the morph map resolves from morph_type.
type morphToModelStub struct {
	ID         uint64 `gorm:"column:id;primaryKey;autoIncrement" db:"id"`
	ForeignKey uint64 `gorm:"column:foreign_key" db:"foreign_key"`
	MorphType  string `gorm:"column:morph_type" db:"morph_type"`

	Relation any `gorm:"-" db:"-"`
}

func (morphToModelStub) TableName() string { return "eloquent_morph_to_model_stubs" }

func (morphToModelStub) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Relation": contractsorm.MorphTo{Name: "relation", TypeColumn: "morph_type", IDColumn: "foreign_key"},
	}
}

// morphToRelatedStub ports EloquentMorphToRelatedStub: the corpus's related table, which carries
// only its auto-incrementing key. MorphClass() is the Go form of getMorphClass(): Laravel writes
// the model's class name into the morph type column when nothing is aliased, so this model names
// that value explicitly — the string associate() writes and the morph map resolves back.
type morphToRelatedStub struct {
	ID uint64 `gorm:"column:id;primaryKey;autoIncrement" db:"id"`
}

func (morphToRelatedStub) TableName() string { return "eloquent_morph_to_related_stubs" }

func (morphToRelatedStub) MorphClass() string { return "eloquent_morph_to_related_stub" }

// morphToDefaultParent declares the corpus's withDefault() MorphTo: a missing target loads a
// fresh instance whose class follows the type column — an empty type column yields the parent's
// own class, so this type carries the username column the corpus's dynamic / array defaults fill.
// morphToDynamicDefaultParent and morphToArrayDefaultParent carry the closure / array forms.
type morphToDefaultParent struct {
	ID         uint64 `gorm:"column:id;primaryKey;autoIncrement" db:"id"`
	ForeignKey uint64 `gorm:"column:foreign_key" db:"foreign_key"`
	MorphType  string `gorm:"column:morph_type" db:"morph_type"`
	Username   string `gorm:"column:username" db:"username"`

	Relation any `gorm:"-" db:"-"`
}

func (morphToDefaultParent) TableName() string { return "eloquent_morph_to_model_stubs" }

func (morphToDefaultParent) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Relation": contractsorm.MorphTo{Name: "relation", TypeColumn: "morph_type", IDColumn: "foreign_key", WithDefault: true},
	}
}

// morphToDynamicDefaultParent is withDefault(fn ($model) => $model->username = 'taylor').
type morphToDynamicDefaultParent struct {
	ID         uint64 `gorm:"column:id;primaryKey;autoIncrement" db:"id"`
	ForeignKey uint64 `gorm:"column:foreign_key" db:"foreign_key"`
	MorphType  string `gorm:"column:morph_type" db:"morph_type"`
	Username   string `gorm:"column:username" db:"username"`

	Relation any `gorm:"-" db:"-"`
}

func (morphToDynamicDefaultParent) TableName() string { return "eloquent_morph_to_model_stubs" }

func (morphToDynamicDefaultParent) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Relation": contractsorm.MorphTo{
			Name: "relation", TypeColumn: "morph_type", IDColumn: "foreign_key",
			WithDefault: func(related, parent any) any {
				related.(*morphToDynamicDefaultParent).Username = "taylor"
				return nil
			},
		},
	}
}

// morphToArrayDefaultParent is withDefault(['username' => 'taylor']).
type morphToArrayDefaultParent struct {
	ID         uint64 `gorm:"column:id;primaryKey;autoIncrement" db:"id"`
	ForeignKey uint64 `gorm:"column:foreign_key" db:"foreign_key"`
	MorphType  string `gorm:"column:morph_type" db:"morph_type"`
	Username   string `gorm:"column:username" db:"username"`

	Relation any `gorm:"-" db:"-"`
}

func (morphToArrayDefaultParent) TableName() string { return "eloquent_morph_to_model_stubs" }

func (morphToArrayDefaultParent) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Relation": contractsorm.MorphTo{Name: "relation", TypeColumn: "morph_type", IDColumn: "foreign_key", WithDefault: map[string]any{"username": "taylor"}},
	}
}

// init registers the related model in the process-wide morph map under the value MorphClass()
// writes into morph_type — the registry the MorphTo loader resolves back to *morphToRelatedStub.
// Laravel stores the model class name and resolves it through class_exists(); the morph map is
// the Go counterpart (the corpus's stub class is likewise resolved by name).
func init() {
	morphmap.Register(map[string]any{
		"eloquent_morph_to_related_stub":           &morphToRelatedStub{},
		"eloquent_morph_to_related_stub_connected": &morphToRelatedConnectedStub{},
	})
}

// --- schema and seeding ------------------------------------------------------

// morphToSchema mirrors the stub tables the corpus's getRelation() implies: the parent carries
// foreign_key (the morph id column) and morph_type; the related table carries only its id.
func morphToSchema(t *testing.T, query contractsorm.Query) {
	t.Helper()

	runRelationSchema(t, query,
		`create table eloquent_morph_to_model_stubs (id integer primary key autoincrement, foreign_key integer null, morph_type varchar null)`,
		`create table eloquent_morph_to_related_stubs (id integer primary key autoincrement)`,
	)
}

// morphToCreateParent inserts a parent row carrying only its key, the state from which the
// associate test starts (no relation written yet).
func morphToCreateParent(t *testing.T, query contractsorm.Query, id uint64) morphToModelStub {
	t.Helper()

	parent := morphToModelStub{ID: id}
	require.NoError(t, cloneRelationQuery(t, query).Model(&parent).Create(map[string]any{"id": id}))

	return parent
}

// morphToCreateRelated inserts a related row. The table has only an auto-incrementing id, and the
// ORM compiles an all-defaults struct insert as `insert into "..." () values ()`, which sqlite
// rejects, so the explicit id the auto increment would assign is written through the map form —
// the same row Laravel's create() would produce.
func morphToCreateRelated(t *testing.T, query contractsorm.Query, id uint64) morphToRelatedStub {
	t.Helper()

	related := morphToRelatedStub{ID: id}
	require.NoError(t, cloneRelationQuery(t, query).Model(&related).Create(map[string]any{"id": id}))

	return related
}

// --- ports of the corpus -----------------------------------------------------

func TestMorphTo_AssociateMethodSetsForeignKeyAndTypeOnModel(t *testing.T) {
	query := newRelationQuery(t)
	morphToSchema(t, query)

	parent := morphToCreateParent(t, query, 1)
	related := morphToCreateRelated(t, query, 1)

	// Laravel (mocked): $associate returns 1 for its key and the morph class for getMorphClass(),
	// and the relation sets $parent->foreign_key = 1, $parent->morph_type = the morph class and
	// setRelation('relation', $associate).
	require.NoError(t, cloneRelationQuery(t, query).Relation(&parent, "Relation").Associate(&related))

	// The parent's foreign key (the morph id column) takes the associate's key, and the morph type
	// column takes its morph class — MorphClass() here, the value Laravel's getMorphClass()
	// returns.
	assert.Equal(t, related.ID, parent.ForeignKey)
	assert.Equal(t, "eloquent_morph_to_related_stub", parent.MorphType)

	// The Go writer also persists the parent (Laravel's associate() only mutates the in-memory
	// attributes), so the row carries the same values.
	reloaded := morphToModelStub{}
	require.NoError(t, cloneRelationQuery(t, query).Where("id", parent.ID).First(&reloaded))
	assert.Equal(t, related.ID, reloaded.ForeignKey)
	assert.Equal(t, "eloquent_morph_to_related_stub", reloaded.MorphType)

	// Laravel marks the relation loaded with the associate (setRelation). The Go writer leaves the
	// relation field alone, so the same observable is a fresh Load, which must resolve to the
	// associate.
	require.NoError(t, cloneRelationQuery(t, query).Load(&reloaded, "Relation"))
	require.NotNil(t, reloaded.Relation)
	loaded, ok := reloaded.Relation.(*morphToRelatedStub)
	require.True(t, ok, "the MorphTo load must resolve eloquent_morph_to_related_stubs through the morph map")
	assert.Equal(t, related.ID, loaded.ID)
}

func TestMorphTo_DissociateMethodDeletesUnsetsKeyAndTypeOnModel(t *testing.T) {
	query := newRelationQuery(t)
	morphToSchema(t, query)

	// Seed an associated parent: foreign_key points at the related row, morph_type names its morph
	// class.
	parent := morphToModelStub{ID: 1}
	require.NoError(t, cloneRelationQuery(t, query).Model(&parent).Create(map[string]any{
		"id": uint64(1), "foreign_key": uint64(1), "morph_type": "eloquent_morph_to_related_stub",
	}))

	// Laravel (mocked): $parent->foreign_key = null, $parent->morph_type = null and
	// setRelation('relation', null). The Go model's columns are plain values, so Laravel's null
	// is the field's zero value here.
	require.NoError(t, cloneRelationQuery(t, query).Relation(&parent, "Relation").Dissociate())
	assert.Zero(t, parent.ForeignKey)
	assert.Zero(t, parent.MorphType)

	// The Go writer also persists the cleared parent (Laravel's dissociate() only mutates the
	// in-memory attributes).
	reloaded := morphToModelStub{}
	require.NoError(t, cloneRelationQuery(t, query).Where("id", parent.ID).First(&reloaded))
	assert.Zero(t, reloaded.ForeignKey)
	assert.Zero(t, reloaded.MorphType)

	// Laravel: setRelation('relation', null) — the relation is gone; a fresh Load of the cleared
	// parent resolves to nil.
	require.NoError(t, cloneRelationQuery(t, query).Load(&reloaded, "Relation"))
	assert.Nil(t, reloaded.Relation)
}

func TestMorphTo_LookupDictionaryIsProperlyConstructedForEnums(t *testing.T) {
	t.Skip("PHP-only assertion (no Go equivalent): asserts MorphTo::addEagerConstraints fills the relation's internal getDictionary() keyed by the enum morph type and its value — relation-object internals the Go eager loader keeps private inside loadMorphTo, and PHP enums have no Go counterpart. Recorded residue (morph-to-surface round), oracle-executed evidence: php .claude/laravel-oracle/oracle.php /tmp/morphport-probes/dictionary.php (illuminate/database v12.55.1) prints enum dictionary keys ['MpdPost'] with the 9 key listing two instances — the dictionary lives only on the relation instance; the Go loader groups the same parents privately in loadMorphTo (eager_loader.go) and exposes no accessor")
}

func TestMorphTo_LookupDictionaryIsProperlyConstructed(t *testing.T) {
	t.Skip("PHP-only assertion (no Go equivalent): asserts getDictionary() groups parents by (morph type, stringified foreign key) with duplicate entries listed under their key — relation-object internals; the Go loader builds the same per-type grouping privately in loadMorphTo and exposes no dictionary accessor. Recorded residue (morph-to-surface round), oracle-executed evidence: php .claude/laravel-oracle/oracle.php /tmp/morphport-probes/dictionary.php (illuminate/database v12.55.1) prints ['MpdPost' => [5 => [0 => comment, 1 => comment]], 'MpdVideo' => [7 => [0 => comment]]] and preserves the associative collection's keys (['a' => ..., 'b' => ...])")
}

func TestMorphTo_MorphToWithDefault(t *testing.T) {
	query := newRelationQuery(t)
	morphToSchema(t, query)

	// Laravel: withDefault()->getResults() returns a new related instance when the relation query
	// finds nothing. Oracle (empty type column): newRelatedInstanceFor re-invokes
	// $parent->relation(), whose null-type branch is morphEagerTo — the fresh instance is of the
	// parent's own class, attributes [].
	parent := morphToDefaultParent{ID: 1}
	require.NoError(t, cloneRelationQuery(t, query).Model(&parent).Create(map[string]any{"id": uint64(1)}))

	require.NoError(t, cloneRelationQuery(t, query).Load(&parent, "Relation"))
	require.NotNil(t, parent.Relation)
	fallback, ok := parent.Relation.(*morphToDefaultParent)
	require.True(t, ok, "an empty type column resolves the default to the parent's own class")
	assert.Zero(t, fallback.ID)
	assert.Empty(t, fallback.MorphType)
	assert.Empty(t, fallback.Username)

	// Oracle (existing row): the matched row wins over the default. Load runs against the model
	// it is handed, so the struct carries the row's values as well as the insert map writing them.
	related := morphToCreateRelated(t, query, 1)
	linked := morphToDefaultParent{ID: 2, ForeignKey: 1, MorphType: "eloquent_morph_to_related_stub"}
	require.NoError(t, cloneRelationQuery(t, query).Model(&linked).Create(map[string]any{
		"id": uint64(2), "foreign_key": uint64(1), "morph_type": "eloquent_morph_to_related_stub",
	}))
	require.NoError(t, cloneRelationQuery(t, query).Load(&linked, "Relation"))
	loaded, ok := linked.Relation.(*morphToRelatedStub)
	require.True(t, ok, "a matched morph target must win over the default")
	assert.Equal(t, related.ID, loaded.ID)

	// Oracle (missing target): with a type column set, the default's class follows the type — the
	// fresh instance is of the related stub, attributes [] — and the query miss does not error.
	missing := morphToDefaultParent{ID: 3, ForeignKey: 99, MorphType: "eloquent_morph_to_related_stub"}
	require.NoError(t, cloneRelationQuery(t, query).Model(&missing).Create(map[string]any{
		"id": uint64(3), "foreign_key": uint64(99), "morph_type": "eloquent_morph_to_related_stub",
	}))
	require.NoError(t, cloneRelationQuery(t, query).Load(&missing, "Relation"))
	missingRelated, ok := missing.Relation.(*morphToRelatedStub)
	require.True(t, ok)
	assert.Zero(t, missingRelated.ID)

	// Oracle (eager): initRelation seeds every parent's default and match overwrites the matched
	// rows — the empty-type parent keeps the parent-class instance, the matched parent the real
	// row, and the missing target the type-class instance.
	foundParents := []morphToDefaultParent{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&morphToDefaultParent{}).With("Relation").OrderBy("id", "asc").Get(&foundParents))
	require.Len(t, foundParents, 3)
	fallbackEager, ok := foundParents[0].Relation.(*morphToDefaultParent)
	require.True(t, ok)
	assert.Zero(t, fallbackEager.ID)
	linkedEager, ok := foundParents[1].Relation.(*morphToRelatedStub)
	require.True(t, ok)
	assert.Equal(t, related.ID, linkedEager.ID)
	missingEager, ok := foundParents[2].Relation.(*morphToRelatedStub)
	require.True(t, ok)
	assert.Zero(t, missingEager.ID)
}

func TestMorphTo_MorphToWithDynamicDefault(t *testing.T) {
	query := newRelationQuery(t)
	morphToSchema(t, query)

	// Laravel: withDefault(fn ($model) => $model->username = 'taylor') fills the freshly built
	// default instance through the callback. Oracle: an empty type column makes the instance the
	// parent's own class, and the callback's nil return keeps it — {"username":"taylor"}.
	parent := morphToDynamicDefaultParent{ID: 1}
	require.NoError(t, cloneRelationQuery(t, query).Model(&parent).Create(map[string]any{"id": uint64(1)}))

	require.NoError(t, cloneRelationQuery(t, query).Load(&parent, "Relation"))
	require.NotNil(t, parent.Relation)
	filled, ok := parent.Relation.(*morphToDynamicDefaultParent)
	require.True(t, ok)
	assert.Equal(t, "taylor", filled.Username)
}

func TestMorphTo_MorphToWithArrayDefault(t *testing.T) {
	query := newRelationQuery(t)
	morphToSchema(t, query)

	// Laravel: withDefault(['username' => 'taylor']) fills the freshly built default instance from
	// the array. Oracle: force-fill yields {"username":"taylor"} on the parent-class instance.
	parent := morphToArrayDefaultParent{ID: 1}
	require.NoError(t, cloneRelationQuery(t, query).Model(&parent).Create(map[string]any{"id": uint64(1)}))

	require.NoError(t, cloneRelationQuery(t, query).Load(&parent, "Relation"))
	require.NotNil(t, parent.Relation)
	filled, ok := parent.Relation.(*morphToArrayDefaultParent)
	require.True(t, ok)
	assert.Equal(t, "taylor", filled.Username)
}

func TestMorphTo_MorphToWithZeroMorphType(t *testing.T) {
	t.Skip("PHP-only assertion (no Go equivalent): asserts Model::morphTo() routes a 0 type through morphInstanceTo() (class-name dispatch) and never through morphEagerTo() — a construction-time branch of the PHP relation; the Go ORM declares the MorphTo once and resolves each row's type through the morph map at load time. Recorded residue (morph-to-surface round), oracle-executed evidence: php .claude/laravel-oracle/oracle.php /tmp/morphport-probes/construction.php (illuminate/database v12.55.1) — an int 0 type takes the instance form (query model = the morph-mapped class, getOwnerKeyName() 'id'; unmapped, class resolution fails with PHP's class-name error)")
}

func TestMorphTo_MorphToWithEmptyStringMorphType(t *testing.T) {
	t.Skip("PHP-only assertion (no Go equivalent): asserts Model::morphTo() routes an empty-string type through morphEagerTo() and never through morphInstanceTo() — the same construction-time branch as the zero-type test, with no Go counterpart. Recorded residue (morph-to-surface round), oracle-executed evidence: php .claude/laravel-oracle/oracle.php /tmp/morphport-probes/construction.php — the null-type and empty-string-type forms both build the eager shape (query model = the declaring class, getOwnerKeyName() NULL)")
}

func TestMorphTo_MorphToWithSpecifiedClassDefault(t *testing.T) {
	query := newRelationQuery(t)
	morphToSchema(t, query)

	// Laravel: $parent->relation_type = EloquentMorphToRelatedStub::class, then
	// $parent->relation()->withDefault()->getResults() — this test additionally pins that the
	// default instance's class follows the type column (EloquentMorphToRelatedStub; the Go port
	// resolves the same value through the morph map). Oracle (specified class, null id): the fresh
	// instance is of the type's class and carries no attributes; the null foreign key
	// short-circuits before any query runs.
	parent := morphToDefaultParent{ID: 1, MorphType: "eloquent_morph_to_related_stub"}
	require.NoError(t, cloneRelationQuery(t, query).Model(&parent).Create(map[string]any{
		"id": uint64(1), "morph_type": "eloquent_morph_to_related_stub",
	}))

	require.NoError(t, cloneRelationQuery(t, query).Load(&parent, "Relation"))
	require.NotNil(t, parent.Relation)
	related, ok := parent.Relation.(*morphToRelatedStub)
	require.True(t, ok, "the default instance's class must follow the morph type column")
	assert.Zero(t, related.ID)
}

func TestMorphTo_AssociateMethodIgnoresNullValue(t *testing.T) {
	query := newRelationQuery(t)
	morphToSchema(t, query)

	// Seed an associated parent: foreign_key points at a related row, morph_type names its morph
	// class (the Dissociate port's fixture).
	parent := morphToModelStub{ID: 1}
	require.NoError(t, cloneRelationQuery(t, query).Model(&parent).Create(map[string]any{
		"id": uint64(1), "foreign_key": uint64(1), "morph_type": "eloquent_morph_to_related_stub",
	}))

	// Laravel (mocked): $parent->foreign_key = null, $parent->morph_type = null and
	// setRelation('relation', null); the null value is never looked up as a model (hence the
	// test's name). The Go writer also persists the cleared parent, its documented divergence.
	require.NoError(t, cloneRelationQuery(t, query).Relation(&parent, "Relation").Associate(nil))

	assert.Zero(t, parent.ForeignKey)
	assert.Zero(t, parent.MorphType)

	reloaded := morphToModelStub{}
	require.NoError(t, cloneRelationQuery(t, query).Where("id", parent.ID).First(&reloaded))
	assert.Zero(t, reloaded.ForeignKey)
	assert.Zero(t, reloaded.MorphType)

	// Laravel: setRelation('relation', null) — the relation is gone; a fresh Load of the cleared
	// parent resolves to nil.
	require.NoError(t, cloneRelationQuery(t, query).Load(&reloaded, "Relation"))
	assert.Nil(t, reloaded.Relation)
}

// --- relation-object is() / isNot() ------------------------------------------

// The corpus pins MorphTo::is() through mocked Models: the parent carries foreign_key 1 and a
// morph_type naming the related stub, and the compared model returns an id plus its table and
// connection name. The relation object Query.Related returns now carries the same comparison, so
// each test below asserts it for real. The connection cases resolve a related class declaring
// 'relation' — the corpus's mock connection name — through its own morph alias, so the equality
// weighs a non-empty value.

// morphToRelatedConnectedStub is the corpus's related mock: it shares the related table under the
// morph alias the is() tests use and declares connection 'relation'.
type morphToRelatedConnectedStub struct {
	ID uint64 `gorm:"column:id;primaryKey;autoIncrement" db:"id"`
}

func (morphToRelatedConnectedStub) TableName() string  { return "eloquent_morph_to_related_stubs" }
func (morphToRelatedConnectedStub) Connection() string { return "relation" }
func (morphToRelatedConnectedStub) MorphClass() string {
	return "eloquent_morph_to_related_stub_connected"
}

// morphToStringFkParent carries a text morph id — the corpus's string parent key.
type morphToStringFkParent struct {
	ID         uint64 `gorm:"column:id;primaryKey;autoIncrement" db:"id"`
	ForeignKey string `gorm:"column:foreign_key" db:"foreign_key"`
	MorphType  string `gorm:"column:morph_type" db:"morph_type"`
}

func (morphToStringFkParent) TableName() string { return "eloquent_morph_to_model_stubs" }

func (morphToStringFkParent) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Relation": contractsorm.MorphTo{Name: "relation", TypeColumn: "morph_type", IDColumn: "foreign_key"},
	}
}

// morphToStringKeyRelatedStub shares the related table under a text key — the corpus's string
// model key.
type morphToStringKeyRelatedStub struct {
	ID string `gorm:"column:id;primaryKey" db:"id"`
}

func (morphToStringKeyRelatedStub) TableName() string  { return "eloquent_morph_to_related_stubs" }
func (morphToStringKeyRelatedStub) Connection() string { return "relation" }

// morphToOtherTableRelatedStub shares the connection but not the table — the corpus's 'table.two'
// model.
type morphToOtherTableRelatedStub struct {
	ID uint64 `gorm:"column:id;primaryKey;autoIncrement" db:"id"`
}

func (morphToOtherTableRelatedStub) TableName() string  { return "eloquent_morph_to_related_stubs.two" }
func (morphToOtherTableRelatedStub) Connection() string { return "relation" }

// morphToOtherConnRelatedStub shares the table but not the connection — the corpus's
// 'connection.two' model.
type morphToOtherConnRelatedStub struct {
	ID uint64 `gorm:"column:id;primaryKey;autoIncrement" db:"id"`
}

func (morphToOtherConnRelatedStub) TableName() string  { return "eloquent_morph_to_related_stubs" }
func (morphToOtherConnRelatedStub) Connection() string { return "relation.two" }

func TestMorphTo_IsNotNull(t *testing.T) {
	query := newRelationQuery(t)

	// Laravel: asserts is(null) is false without reading the relation's related model at all
	// (getTable / getConnectionName never called) — the Go comparison short-circuits on the nil
	// model identically (oracle: mt is(null) false).
	parent := morphToModelStub{ID: 1, ForeignKey: 1, MorphType: "eloquent_morph_to_related_stub_connected"}
	rel := cloneRelationQuery(t, query).Related(&parent, "Relation").(contractsorm.MorphToRelation)
	assert.False(t, rel.Is(nil))
}

func TestMorphTo_IsModel(t *testing.T) {
	query := newRelationQuery(t)

	// Laravel: asserts is() is true when the model's key equals the parent's foreign key and the
	// model's table and connection name match the relation's related model (oracle: mt conn
	// is(conn id 1) true).
	parent := morphToModelStub{ID: 1, ForeignKey: 1, MorphType: "eloquent_morph_to_related_stub_connected"}
	rel := cloneRelationQuery(t, query).Related(&parent, "Relation").(contractsorm.MorphToRelation)
	assert.True(t, rel.Is(&morphToRelatedConnectedStub{ID: 1}))
}

func TestMorphTo_IsModelWithIntegerParentKey(t *testing.T) {
	query := newRelationQuery(t)

	// Laravel: asserts is() compares an integer parent key with the model's string key numerically
	// (ComparesRelatedModels' loose compareKeys cast) (oracle: mt is(id "1") true).
	parent := morphToModelStub{ID: 1, ForeignKey: 1, MorphType: "eloquent_morph_to_related_stub_connected"}
	rel := cloneRelationQuery(t, query).Related(&parent, "Relation").(contractsorm.MorphToRelation)
	assert.True(t, rel.Is(&morphToStringKeyRelatedStub{ID: "1"}))
}

func TestMorphTo_IsModelWithIntegerRelatedKey(t *testing.T) {
	query := newRelationQuery(t)

	// Laravel: asserts is() compares a string parent key with the model's integer key numerically
	// (oracle: mt str-fk is(id 1) true).
	parent := morphToStringFkParent{ID: 1, ForeignKey: "1", MorphType: "eloquent_morph_to_related_stub_connected"}
	rel := cloneRelationQuery(t, query).Related(&parent, "Relation").(contractsorm.MorphToRelation)
	assert.True(t, rel.Is(&morphToRelatedConnectedStub{ID: 1}))
}

func TestMorphTo_IsModelWithIntegerKeys(t *testing.T) {
	query := newRelationQuery(t)

	// Laravel: asserts is() is true for equal integer keys on both sides (oracle: mt is(id 1)
	// true).
	parent := morphToModelStub{ID: 1, ForeignKey: 1, MorphType: "eloquent_morph_to_related_stub_connected"}
	rel := cloneRelationQuery(t, query).Related(&parent, "Relation").(contractsorm.MorphToRelation)
	assert.True(t, rel.Is(&morphToRelatedConnectedStub{ID: 1}))
}

func TestMorphTo_IsNotModelWithNullParentKey(t *testing.T) {
	query := newRelationQuery(t)

	// Laravel: asserts is() is false when the parent's foreign key is null. The Go morph id is
	// value-typed, so the zero takes the empty-key reading compareKeys gives the PHP null (oracle:
	// mt null-fk is(id 1) false).
	parent := morphToModelStub{ID: 1, MorphType: "eloquent_morph_to_related_stub_connected"}
	rel := cloneRelationQuery(t, query).Related(&parent, "Relation").(contractsorm.MorphToRelation)
	assert.False(t, rel.Is(&morphToRelatedConnectedStub{ID: 1}))
}

func TestMorphTo_IsNotModelWithNullRelatedKey(t *testing.T) {
	query := newRelationQuery(t)

	// Laravel: asserts is() is false when the model's owner key is null (oracle: mt is(id null)
	// false).
	parent := morphToModelStub{ID: 1, ForeignKey: 1, MorphType: "eloquent_morph_to_related_stub_connected"}
	rel := cloneRelationQuery(t, query).Related(&parent, "Relation").(contractsorm.MorphToRelation)
	assert.False(t, rel.Is(&morphToRelatedConnectedStub{}))
}

func TestMorphTo_IsNotModelWithAnotherKey(t *testing.T) {
	query := newRelationQuery(t)

	// Laravel: asserts is() is false when the model's key differs from the parent's foreign key
	// (oracle: mt is(id 2) false).
	parent := morphToModelStub{ID: 1, ForeignKey: 1, MorphType: "eloquent_morph_to_related_stub_connected"}
	rel := cloneRelationQuery(t, query).Related(&parent, "Relation").(contractsorm.MorphToRelation)
	assert.False(t, rel.Is(&morphToRelatedConnectedStub{ID: 2}))
}

func TestMorphTo_IsNotModelWithAnotherTable(t *testing.T) {
	query := newRelationQuery(t)

	// Laravel: asserts is() is false when the model's table differs from the related model's table
	// (oracle: mt is(other table id 1) false).
	parent := morphToModelStub{ID: 1, ForeignKey: 1, MorphType: "eloquent_morph_to_related_stub_connected"}
	rel := cloneRelationQuery(t, query).Related(&parent, "Relation").(contractsorm.MorphToRelation)
	assert.False(t, rel.Is(&morphToOtherTableRelatedStub{ID: 1}))
}

func TestMorphTo_IsNotModelWithAnotherConnection(t *testing.T) {
	query := newRelationQuery(t)

	// Laravel: asserts is() is false when the model's connection name differs from the related
	// model's connection (oracle: mt is(other conn id 1) false).
	parent := morphToModelStub{ID: 1, ForeignKey: 1, MorphType: "eloquent_morph_to_related_stub_connected"}
	rel := cloneRelationQuery(t, query).Related(&parent, "Relation").(contractsorm.MorphToRelation)
	assert.False(t, rel.Is(&morphToOtherConnRelatedStub{ID: 1}))
}
