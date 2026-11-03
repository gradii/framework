package inverse_relation_has_one

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/support/carbon"
)

// This file ports Laravel's tests/Database/DatabaseEloquentInverseRelationHasOneTest.php (12.x) onto
// the framework ORM: one Go test per Laravel test method, named
// TestInverseRelationHasOne_<method without the test prefix>. The corpus's two tables mirror
// createSchema() in sqlite DDL; rows are seeded through the ORM, and relations are loaded
// explicitly through OrmBuilder.Load — the harness's lazy-loading rule, since Go has no
// property-access magic.
//
// # What the corpus asserts
//
// Every test attaches the same pair of assertions to a child acquired through a different path
// (lazy read, eager read, make, create, createQuietly, forceCreate, save, saveQuietly,
// reparenting save):
//
//   - $child->relationLoaded('parent') is true
//   - $child->parent === $parent (object identity)
//
// They hold because child() is declared with ->inverse('parent') — a "chaperone": Eloquent links
// the related model back to its parent as soon as the relationship query has run, and every write
// path sets the relation too. All nine paths were executed against the real Eloquent by the oracle
// (.claude/laravel-oracle/oracle.php against illuminate/database v12.55.1); the comments below
// cite those measurements.
//
// # The inverse feature, and how it is ported
//
// child() is declared with ->inverse('parent') — a "chaperone": Eloquent links the related model
// back to its parent as soon as the relationship query has run. The Go declaration is the
// relation's Inverse field (contracts/database/orm), and the loaders (database/gorm/eager_loader.go)
// write the parent back onto every child they match: after Load(&parent, "Child") — or With — the
// child's Parent holds the very parent instance with no further query (the corpus's
// relationLoaded('parent') + assertSame($parent, $child->parent); oracle-re-verified for the lazy,
// eager, has-one, morph and one-of-many shapes by
// .claude/laravel-oracle/_inverse_writeback_probe.php). The read-path ports below assert exactly
// that via assert.Same.
//
// The corpus's write paths (make, create, createQuietly, forceCreate, save, saveQuietly, the
// reparenting save) rely on HasOneOrMany applying the same link from its writers; the Go writer
// does too — setRelationFKOnChild, the Go form of setForeignAttributesForCreate, ends with
// SupportsInverseRelations::applyInverseRelationToModel (HasOneOrMany.php:506) — so those ports
// assert the corpus pair directly, like the read paths. Freshly probed (oracle, illuminate/database
// v12.55.1, executed, /tmp/lalign-defaults-inverse/inverse_writeback_runtime.php): create / make /
// save / forceCreate and the Many variants all report same-instance true.
//
// The writer carries Make/MakeMany — an unsaved child or children with the FK preset and the
// inverse link, nothing persisted — and the quiet (event-suppressed) / force
// (mass-assignment-bypassing) write variants still have no flat counterpart (the relation object
// Related(...) carries CreateQuietly / CreateManyQuietly / ForceCreate family). The quiet/force
// ports run the shared Create / Save path (nothing in this corpus registers observers or
// distinguishes the variants); the making port now asserts the corpus pair directly, Make linking
// the unsaved child's Parent in memory.
//
// # Oracle measurements behind the ports
//
// The lazy $parent->child read is `select * from "test_child" where "test_child"."parent_id" = ?
// and "test_child"."parent_id" is not null limit 1`; the chaperoned $child->parent runs no query (a
// non-chaperoned belongsTo property read is `select * from "test_parent" where "test_parent"."id" =
// ? limit 1`); with('child') runs `select * from "test_parent"` plus `select * from "test_child"
// where "test_child"."parent_id" in (1, 2, 3, 4, 5)`; make() issues no query at all (attributes
// ['parent_id' => n], exists false, yet relationLoaded('parent') is true); create / createQuietly /
// forceCreate / save / saveQuietly all run `insert into "test_child" ("parent_id", "updated_at",
// "created_at") values (?, ?, ?)`; the reparenting save runs `update "test_child" set "parent_id" =
// ?, "updated_at" = ? where "id" = ?`.

// --- models ------------------------------------------------------------------

// inverseHasOneParent ports HasOneInverseParentModel: test_parent holds the key, and the child
// destination field is declared gorm:"-" and named by the Relations() map key.
type inverseHasOneParent struct {
	ID        uint             `gorm:"column:id;primaryKey;autoIncrement"`
	CreatedAt *carbon.DateTime `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`

	Child *inverseHasOneChild `gorm:"-"`
}

func (inverseHasOneParent) TableName() string { return "test_parent" }

func (inverseHasOneParent) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		// child(): $this->hasOne(Child::class, 'parent_id')->inverse('parent').
		"Child": contractsorm.HasOne{Related: &inverseHasOneChild{}, ForeignKey: "parent_id", Inverse: "Parent"},
	}
}

// inverseHasOneChild ports HasOneInverseChildModel: the table holding parent_id and the BelongsTo
// inverse side.
type inverseHasOneChild struct {
	ID        uint             `gorm:"column:id;primaryKey;autoIncrement"`
	ParentID  uint             `gorm:"column:parent_id"`
	CreatedAt *carbon.DateTime `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`

	Parent *inverseHasOneParent `gorm:"-"`
}

func (inverseHasOneChild) TableName() string { return "test_child" }

func (inverseHasOneChild) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Parent": contractsorm.BelongsTo{Related: &inverseHasOneParent{}, ForeignKey: "parent_id"},
	}
}

// --- schema and seeding ------------------------------------------------------

// inverseHasOneSchema mirrors the corpus's createSchema() in sqlite DDL: test_child.parent_id is
// unique, and both tables carry nullable timestamps.
func inverseHasOneSchema(t *testing.T, query contractsorm.Query) {
	t.Helper()

	runRelationSchema(t, query,
		`create table test_parent (id integer primary key autoincrement, created_at datetime null, updated_at datetime null)`,
		`create table test_child (id integer primary key autoincrement, parent_id integer not null unique, created_at datetime null, updated_at datetime null)`,
	)
}

// inverseHasOneSeedParent ports HasOneInverseParentModel::create(): a parent row and nothing else.
func inverseHasOneSeedParent(t *testing.T, query contractsorm.Query) inverseHasOneParent {
	t.Helper()

	parent := inverseHasOneParent{}
	require.NoError(t, cloneRelationQuery(t, query).Create(&parent))

	return parent
}

// inverseHasOneSeedChild ports HasOneInverseChildModel::factory()->create(): a child row under the
// given parent. The relation writer pre-sets the foreign key before the insert, like Laravel's
// factory and setForeignAttributesForCreate.
func inverseHasOneSeedChild(t *testing.T, query contractsorm.Query, parent *inverseHasOneParent) inverseHasOneChild {
	t.Helper()

	child := inverseHasOneChild{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(parent, "Child").Create(&child))

	return child
}

// inverseHasOneReloadChild reads a child row back from the database.
func inverseHasOneReloadChild(t *testing.T, query contractsorm.Query, id uint) inverseHasOneChild {
	t.Helper()

	reloaded := inverseHasOneChild{}
	require.NoError(t, cloneRelationQuery(t, query).Where("id", id).First(&reloaded))

	return reloaded
}

// inverseHasOneAssertChaperoned asserts the corpus's inverse pair for a child the loaders
// returned: assertTrue($child->relationLoaded('parent')) and assertSame($parent, $child->parent) —
// the load write-back stores the parent instance itself, so the child's relation costs no query.
func inverseHasOneAssertChaperoned(t *testing.T, child *inverseHasOneChild, parent *inverseHasOneParent) {
	t.Helper()

	assert.Equal(t, parent.ID, child.ParentID)
	require.NotNil(t, child.Parent, "the inverse declaration must link the child back to the parent")
	assert.Same(t, parent, child.Parent)
}

// --- ports of the corpus -----------------------------------------------------

func TestInverseRelationHasOne_HasOneInverseRelationIsProperlySetToParentWhenLazyLoaded(t *testing.T) {
	query := newRelationQuery(t)
	inverseHasOneSchema(t, query)

	// HasOneInverseChildModel::factory(5)->create(): five parents, each with one child.
	for i := 0; i < 5; i++ {
		parent := inverseHasOneSeedParent(t, query)
		_ = inverseHasOneSeedChild(t, query, &parent)
	}

	parents := []inverseHasOneParent{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&inverseHasOneParent{}).Get(&parents))
	require.Len(t, parents, 5)

	for i := range parents {
		parent := &parents[i]

		// Laravel: assertFalse($parent->relationLoaded('child')) — the Go equivalent of "not
		// loaded" is the nil destination field on the freshly read parent.
		require.Nil(t, parent.Child)

		// Laravel reads $parent->child (oracle: select * from "test_child" where
		// "test_child"."parent_id" = ? and "test_child"."parent_id" is not null limit 1).
		require.NoError(t, cloneRelationQuery(t, query).Load(parent, "Child"))
		require.NotNil(t, parent.Child)

		// Laravel: assertTrue($child->relationLoaded('parent')) and assertSame($parent,
		// $child->parent) — the inverse() chaperone sets the relation in memory with no query;
		// the loader write-back does the same.
		inverseHasOneAssertChaperoned(t, parent.Child, parent)
	}
}

func TestInverseRelationHasOne_HasOneInverseRelationIsProperlySetToParentWhenEagerLoaded(t *testing.T) {
	query := newRelationQuery(t)
	inverseHasOneSchema(t, query)

	for i := 0; i < 5; i++ {
		parent := inverseHasOneSeedParent(t, query)
		_ = inverseHasOneSeedChild(t, query, &parent)
	}

	// Laravel: HasOneInverseParentModel::with('child')->get(). Get drains its cursor before the
	// eager query runs, so it is used instead of First, which would keep its single-row cursor
	// open and deadlock on the harness's one-connection pool.
	parents := []inverseHasOneParent{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&inverseHasOneParent{}).With("Child").Get(&parents))
	require.Len(t, parents, 5)

	for i := range parents {
		parent := &parents[i]
		require.NotNil(t, parent.Child, "with(\"Child\") must attach every parent's child")

		// Laravel: assertTrue($child->relationLoaded('parent')) / assertSame($parent,
		// $child->parent), applied by the chaperone during matchOne — the loader write-back.
		inverseHasOneAssertChaperoned(t, parent.Child, parent)
	}
}

func TestInverseRelationHasOne_HasOneInverseRelationIsProperlySetToParentWhenMaking(t *testing.T) {
	query := newRelationQuery(t)
	inverseHasOneSchema(t, query)

	parent := inverseHasOneSeedParent(t, query)

	// Laravel: $child = $parent->child()->make() — a fresh, unsaved child with the FK preset and
	// the inverse relation wired (oracle: relationLoaded true, parent identical, attributes
	// ['parent_id' => n], exists false, no query; probe
	// /tmp/lalign-defaults-inverse/inverse_writeback_runtime.php 2: make.same-instance true).
	child := inverseHasOneChild{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&parent, "Child").Make(map[string]any{}, &child))

	assert.Zero(t, child.ID, "make() persists nothing")
	assert.Equal(t, parent.ID, child.ParentID)
	require.NotNil(t, child.Parent, "make() must link the child back to the parent")
	assert.Same(t, &parent, child.Parent)
}

func TestInverseRelationHasOne_HasOneInverseRelationIsProperlySetToParentWhenCreating(t *testing.T) {
	query := newRelationQuery(t)
	inverseHasOneSchema(t, query)

	parent := inverseHasOneSeedParent(t, query)

	// Laravel: $child = $parent->child()->create(). setForeignAttributesForCreate writes the FK
	// before the insert (oracle: insert into "test_child" ("parent_id", "updated_at", "created_at")
	// values (?, ?, ?)).
	child := inverseHasOneChild{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&parent, "Child").Create(&child))
	assert.Equal(t, parent.ID, child.ParentID)

	// The created row is persisted with that foreign key.
	persisted := inverseHasOneReloadChild(t, query, child.ID)
	assert.Equal(t, parent.ID, persisted.ParentID)

	// Laravel: assertTrue($child->relationLoaded('parent')) / assertSame($parent, $child->parent)
	// via the chaperone — create applies applyInverseRelationToModel after the save.
	inverseHasOneAssertChaperoned(t, &child, &parent)
}

func TestInverseRelationHasOne_HasOneInverseRelationIsProperlySetToParentWhenCreatingQuietly(t *testing.T) {
	query := newRelationQuery(t)
	inverseHasOneSchema(t, query)

	parent := inverseHasOneSeedParent(t, query)

	// Laravel: $child = $parent->child()->createQuietly(), create() inside Model::withoutEvents.
	// The Go relation writer has a single Create path — no quiet variant — and no model events are
	// registered on it, so the shared path is behaviorally the same here; the corpus asserts only
	// the chaperone pair, reproduced below (oracle: same insert as create).
	child := inverseHasOneChild{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&parent, "Child").Create(&child))
	assert.Equal(t, parent.ID, child.ParentID)

	persisted := inverseHasOneReloadChild(t, query, child.ID)
	assert.Equal(t, parent.ID, persisted.ParentID)

	inverseHasOneAssertChaperoned(t, &child, &parent)
}

func TestInverseRelationHasOne_HasOneInverseRelationIsProperlySetToParentWhenForceCreating(t *testing.T) {
	query := newRelationQuery(t)
	inverseHasOneSchema(t, query)

	parent := inverseHasOneSeedParent(t, query)

	// Laravel: $child = $parent->child()->forceCreate(), create() with mass assignment forced. The
	// Go ORM has no fillable guarding, so Create is the same observable path (oracle: same insert
	// as create).
	child := inverseHasOneChild{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&parent, "Child").Create(&child))
	assert.Equal(t, parent.ID, child.ParentID)

	persisted := inverseHasOneReloadChild(t, query, child.ID)
	assert.Equal(t, parent.ID, persisted.ParentID)

	inverseHasOneAssertChaperoned(t, &child, &parent)
}

func TestInverseRelationHasOne_HasOneInverseRelationIsProperlySetToParentWhenSaving(t *testing.T) {
	query := newRelationQuery(t)
	inverseHasOneSchema(t, query)

	parent := inverseHasOneSeedParent(t, query)

	// Laravel: $child = HasOneInverseChildModel::make() — an unsaved child whose parent relation is
	// not loaded yet; the Go equivalent state is the nil Parent field on a fresh struct.
	child := inverseHasOneChild{}
	require.Nil(t, child.Parent)

	// Laravel: $parent->child()->save($child). The writer sets the FK and inserts (oracle: insert
	// into "test_child" ("parent_id", "updated_at", "created_at") values (?, ?, ?)).
	require.NoError(t, cloneRelationQuery(t, query).Relation(&parent, "Child").Save(&child))
	assert.Equal(t, parent.ID, child.ParentID)

	persisted := inverseHasOneReloadChild(t, query, child.ID)
	assert.Equal(t, parent.ID, persisted.ParentID)

	// Laravel: assertTrue($child->relationLoaded('parent')) / assertSame($parent, $child->parent)
	// via the chaperone applied by save().
	inverseHasOneAssertChaperoned(t, &child, &parent)
}

func TestInverseRelationHasOne_HasOneInverseRelationIsProperlySetToParentWhenSavingQuietly(t *testing.T) {
	query := newRelationQuery(t)
	inverseHasOneSchema(t, query)

	parent := inverseHasOneSeedParent(t, query)

	child := inverseHasOneChild{}
	require.Nil(t, child.Parent)

	// Laravel: $parent->child()->saveQuietly($child), save() inside Model::withoutEvents. The Go
	// relation writer has a single Save path — no quiet variant — and no model events are
	// registered on it, so the shared path is behaviorally the same here; the corpus asserts only
	// the chaperone pair, reproduced below (oracle: same insert as save).
	require.NoError(t, cloneRelationQuery(t, query).Relation(&parent, "Child").Save(&child))
	assert.Equal(t, parent.ID, child.ParentID)

	persisted := inverseHasOneReloadChild(t, query, child.ID)
	assert.Equal(t, parent.ID, persisted.ParentID)

	inverseHasOneAssertChaperoned(t, &child, &parent)
}

func TestInverseRelationHasOne_HasOneInverseRelationIsProperlySetToParentWhenUpdating(t *testing.T) {
	query := newRelationQuery(t)
	inverseHasOneSchema(t, query)

	parent := inverseHasOneSeedParent(t, query)

	// HasOneInverseChildModel::factory()->create(): the child exists under another parent.
	otherParent := inverseHasOneSeedParent(t, query)
	child := inverseHasOneSeedChild(t, query, &otherParent)

	// Laravel: assertTrue($parent->isNot($child->parent)) — reading $child->parent lazy-loads the
	// belongsTo side (oracle: select * from "test_parent" where "test_parent"."id" = ? limit 1) and
	// it is the other parent.
	require.NoError(t, cloneRelationQuery(t, query).Load(&child, "Parent"))
	require.NotNil(t, child.Parent)
	assert.NotEqual(t, parent.ID, child.Parent.ID)

	// Laravel: $parent->child()->save($child) reparents the child and chaperones it. The Go save
	// writes the new FK in memory and updates the row (oracle: update "test_child" set
	// "parent_id" = ?, "updated_at" = ? where "id" = ?).
	require.NoError(t, cloneRelationQuery(t, query).Relation(&parent, "Child").Save(&child))
	assert.Equal(t, parent.ID, child.ParentID)

	persisted := inverseHasOneReloadChild(t, query, child.ID)
	assert.Equal(t, parent.ID, persisted.ParentID)

	// Laravel: assertTrue($parent->is($child->parent)) and assertSame($parent, $child->parent) —
	// the reparenting save stamps the new parent instance onto the child in memory.
	inverseHasOneAssertChaperoned(t, &child, &parent)
}
