package gorm

import (
	"database/sql"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/errors"
)

// stubWriter is the connection a test that only compiles statement gets: compiling reads a model and
// a grammar, never the pool, so no database has to stand behind it.
func stubWriter(t *testing.T) *sql.DB {
	t.Helper()

	return nil
}

// --- Test fixtures ---------------------------------------------------------

type relUser struct {
	ID      uint
	Name    string
	Books   []*relBook  `gorm:"-"`
	Profile *relProfile `gorm:"-"`
	Roles   []*relRole  `gorm:"-"`
	Houses  []*relHouse `gorm:"-"`
	Logo    *relLogo    `gorm:"-"`
}

func (relUser) TableName() string { return "rel_users" }

func (relUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Books":   contractsorm.HasMany{Related: &relBook{}, ForeignKey: "user_id"},
		"Profile": contractsorm.HasOne{Related: &relProfile{}, ForeignKey: "user_id"},
		"Roles":   contractsorm.Many2Many{Related: &relRole{}, Table: "rel_user_roles"},
		"Houses":  contractsorm.MorphMany{Related: &relHouse{}, Name: "houseable"},
		"Logo":    contractsorm.MorphOne{Related: &relLogo{}, Name: "logoable"},
	}
}

type relBook struct {
	ID       uint
	Title    string
	UserID   uint
	AuthorID uint
	Author   *relUser `gorm:"-"`
}

func (relBook) TableName() string { return "rel_books" }

func (relBook) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Author": contractsorm.BelongsTo{Related: &relUser{}, ForeignKey: "author_id"},
	}
}

type relProfile struct {
	ID     uint
	Bio    string
	UserID uint
}

func (relProfile) TableName() string { return "rel_profiles" }

type relRole struct {
	ID   uint
	Name string
}

func (relRole) TableName() string { return "rel_roles" }

type relHouse struct {
	ID            uint
	Address       string
	HouseableID   uint
	HouseableType string
}

func (relHouse) TableName() string { return "rel_houses" }

type relLogo struct {
	ID           uint
	URL          string
	LogoableID   uint
	LogoableType string
}

func (relLogo) TableName() string { return "rel_logos" }

// relCountry / relPost via relUser participate in a HasManyThrough setup.
type relCountry struct {
	ID   uint
	Name string

	// Posts / FirstPost back the through relations declared below. The eager loader writes the
	// related rows onto these fields, so driving loadThrough needs them on the fixture; the SQL
	// tests never reached the assignment and left them off.
	Posts     []*relPost `gorm:"-"`
	FirstPost *relPost   `gorm:"-"`
}

func (relCountry) TableName() string { return "rel_countries" }

func (relCountry) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Posts": contractsorm.HasManyThrough{
			Related: &relPost{},
			Through: &relUser{},
		},
		"FirstPost": contractsorm.HasOneThrough{
			Related: &relPost{},
			Through: &relUser{},
		},
		"NoRelated": contractsorm.HasManyThrough{},
		"BadKind":   unknownRelation{},
	}
}

// unknownRelation satisfies contractsorm.Relation but isn't one of the known per-kind structs.
// Used to exercise the resolver's default branch (OrmMorphRelationKindUnknown) — a defensive
// path that triggers if someone hand-rolls a Relation impl outside the standard set.
type unknownRelation struct{}

func (unknownRelation) Kind() contractsorm.RelationKind { return "weird" }

type relPost struct {
	ID     uint
	Title  string
	UserID uint
	// RelUserID is the FK to rel_users the through relations resolve on: the convention names
	// the related side's second key "<through-singular>_id" = "rel_user_id", and loadThrough
	// looks the related model's field up by that DB name. The SQL tests only render the join,
	// so the column went unmodelled until the through loader was driven directly.
	RelUserID uint
}

func (relPost) TableName() string { return "rel_posts" }

// --- Pure helpers ----------------------------------------------------------

// --- Schema-dependent helpers ---------------------------------------------

func TestTableNameFor(t *testing.T) {
	// A model that declares no table is a named error naming its type and the method to add: there
	// is no naming-strategy fallback left to name it silently.
	_, err := tableNameFor(&undeclaredTableModel{})
	assert.True(t, errors.Is(err, errors.OrmQueryTableNotSet))
	assert.EqualError(t, err, "*gorm.undeclaredTableModel: Table not set, please add a TableName() method returning the table name")

	// A non-struct value is rejected with the same named error, naming the type it was handed.
	_, err = tableNameFor("not-a-model")
	assert.True(t, errors.Is(err, errors.OrmQueryTableNotSet))
}

// undeclaredTableModel declares no table of its own, so resolving it exercises the enforcement
// error rather than a naming strategy.
type undeclaredTableModel struct {
	ID uint
}

// declaredTableModel states its own table through TableName(), the convention make:model writes.
type declaredTableModel struct {
	ID uint
}

func (declaredTableModel) TableName() string { return "declared_things" }

// pointerReceiverTableModel declares TableName on the pointer receiver. GORM's schema.Parse rejects
// a value argument for this shape, so resolving it proves the framework reads the name itself
// rather than handing the model to GORM.
type pointerReceiverTableModel struct {
	ID uint
}

func (*pointerReceiverTableModel) TableName() string { return "pointer_receiver_things" }

// TestTableNameFor_SliceDestination pins the other shape a query's destination takes: Paginate
// records the slice it was handed as the query's model, and the table belongs to the element type.
// Without that unwrapping the count that Paginate runs first fails with "Table not set" for a model
// that declares its table.
func TestTableNameFor_SliceDestination(t *testing.T) {
	name, err := tableNameFor(&[]declaredTableModel{})
	assert.NoError(t, err)
	assert.Equal(t, "declared_things", name)

	name, err = tableNameFor([]declaredTableModel{})
	assert.NoError(t, err)
	assert.Equal(t, "declared_things", name)

	// The enforcement still holds for a slice of a model that declares no table: the error names
	// the model type, not the slice.
	_, err = tableNameFor(&[]undeclaredTableModel{})
	assert.True(t, errors.Is(err, errors.OrmQueryTableNotSet))
	assert.EqualError(t, err, "*gorm.undeclaredTableModel: Table not set, please add a TableName() method returning the table name")
}

func TestTableNameFor_DeclaredNameWins(t *testing.T) {
	// A model that declares TableName() resolves to exactly that name, with no pluralization
	// applied to it.
	name, err := tableNameFor(&declaredTableModel{})
	assert.NoError(t, err)
	assert.Equal(t, "declared_things", name)

	// The name is returned bare, with no connection prefix applied: tableNameFor takes no connection
	// at all, and applying the prefix belongs to the renderer, exactly once (see
	// TestTablePrefixAppliedOnce).
	assert.NotContains(t, name, "app_")
}

func TestTableNameFor_PointerReceiver(t *testing.T) {
	name, err := tableNameFor(&pointerReceiverTableModel{})
	assert.NoError(t, err)
	assert.Equal(t, "pointer_receiver_things", name)

	// A value argument reaches the pointer-receiver method through an addressable copy.
	name, err = tableNameFor(pointerReceiverTableModel{})
	assert.NoError(t, err)
	assert.Equal(t, "pointer_receiver_things", name)
}

// --- resolveRelation across all kinds -------------------------------------

func TestResolveRelation_Empty(t *testing.T) {
	_, err := resolveRelation(&relUser{}, "")
	assert.True(t, errors.Is(err, errors.OrmQueryEmptyRelation))
}

func TestResolveRelation_NotFound(t *testing.T) {
	_, err := resolveRelation(&relUser{}, "Missing")
	assert.True(t, errors.Is(err, errors.OrmRelationNotFound))
}

func TestResolveRelation_HasMany(t *testing.T) {
	desc, err := resolveRelation(&relUser{}, "Books")
	assert.NoError(t, err)
	assert.Equal(t, relKindHasMany, desc.kind)
	assert.Equal(t, "rel_users", desc.parentTable)
	assert.Equal(t, "rel_books", desc.relatedTable)
	assert.NotEmpty(t, desc.references)
}

func TestResolveRelation_HasOne(t *testing.T) {
	desc, err := resolveRelation(&relUser{}, "Profile")
	assert.NoError(t, err)
	assert.Equal(t, relKindHasOne, desc.kind)
	assert.Equal(t, "rel_profiles", desc.relatedTable)
}

func TestResolveRelation_BelongsTo(t *testing.T) {
	desc, err := resolveRelation(&relBook{}, "Author")
	assert.NoError(t, err)
	assert.Equal(t, relKindBelongsTo, desc.kind)
	assert.Equal(t, "rel_users", desc.relatedTable)
}

func TestResolveRelation_Many2Many(t *testing.T) {
	desc, err := resolveRelation(&relUser{}, "Roles")
	assert.NoError(t, err)
	assert.Equal(t, relKindMany2Many, desc.kind)
	assert.Equal(t, "rel_user_roles", desc.pivotTable)
	assert.Equal(t, "rel_users", desc.pivotParentRef.primaryTable)
	assert.Equal(t, "rel_roles", desc.pivotRelatedRef.primaryTable)
}

func TestResolveRelation_MorphMany(t *testing.T) {
	desc, err := resolveRelation(&relUser{}, "Houses")
	assert.NoError(t, err)
	assert.Equal(t, relKindMorphMany, desc.kind)
	assert.Equal(t, "houseable_type", desc.morphTypeColumn)
	assert.Equal(t, "houseable_id", desc.morphIDColumn)
	assert.NotEmpty(t, desc.references)
}

func TestResolveRelation_MorphOne(t *testing.T) {
	desc, err := resolveRelation(&relUser{}, "Logo")
	assert.NoError(t, err)
	assert.Equal(t, relKindMorphOne, desc.kind)
	assert.Equal(t, "logoable_type", desc.morphTypeColumn)
}

func TestResolveRelation_Nested(t *testing.T) {
	desc, err := resolveRelation(&relUser{}, "Books.Author")
	assert.NoError(t, err)
	assert.Equal(t, "Books", desc.name)
	assert.NotNil(t, desc.nested)
	assert.Equal(t, "Author", desc.nested.name)
	assert.Equal(t, relKindBelongsTo, desc.nested.kind)
}

func TestResolveRelation_HasManyThrough(t *testing.T) {
	desc, err := resolveRelation(&relCountry{}, "Posts")
	assert.NoError(t, err)
	assert.Equal(t, relKindHasManyThrough, desc.kind)
	assert.Equal(t, "rel_posts", desc.relatedTable)
	assert.Equal(t, "rel_users", desc.throughTable)
	// Through default keys come from naming conventions:
	//   firstKey  = singular(parentTable) + "_id"
	//   secondKey = singular(throughTable) + "_id"
	//   localKey / secondLocalKey default to "id".
	assert.Equal(t, "rel_country_id", desc.firstKey)
	assert.Equal(t, "rel_user_id", desc.secondKey)
	assert.Equal(t, "id", desc.localKey)
	assert.Equal(t, "id", desc.secondLocalKey)
}

func TestResolveRelation_HasOneThrough(t *testing.T) {
	desc, err := resolveRelation(&relCountry{}, "FirstPost")
	assert.NoError(t, err)
	assert.Equal(t, relKindHasOneThrough, desc.kind)
}

func TestResolveRelation_ThroughNotConfigured(t *testing.T) {
	_, err := resolveRelation(&relCountry{}, "NoRelated")
	assert.True(t, errors.Is(err, errors.OrmRelationThroughNotConfigured))
}

func TestResolveRelation_ThroughBadKind(t *testing.T) {
	_, err := resolveRelation(&relCountry{}, "BadKind")
	assert.True(t, errors.Is(err, errors.OrmMorphRelationKindUnknown))
}

func TestResolveRelation_ThroughNotImplemented(t *testing.T) {
	// relUser does NOT implement ModelWithThroughRelations.
	_, err := resolveRelation(&relUser{}, "Anything")
	assert.True(t, errors.Is(err, errors.OrmRelationNotFound))
}

// Sanity: relatedModel is a fresh pointer to the related struct type.
func TestResolveRelation_RelatedModelType(t *testing.T) {
	desc, err := resolveRelation(&relUser{}, "Books")
	assert.NoError(t, err)
	rt := reflect.TypeOf(desc.relatedModel)
	assert.Equal(t, reflect.Pointer, rt.Kind())
	assert.Equal(t, "relBook", rt.Elem().Name())
}

// --- Morph relation fixtures ---

type morphImage struct {
	ID            uint
	URL           string
	ImageableID   uint
	ImageableType string
	Imageable     any `gorm:"-"`
}

func (morphImage) TableName() string { return "morph_images" }

func (morphImage) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Imageable": contractsorm.MorphTo{Name: "imageable"},
	}
}

type morphPost struct {
	ID    uint
	Title string
	Tags  []*morphTag `gorm:"-"`
}

func (morphPost) TableName() string { return "morph_posts" }

func (morphPost) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Tags": contractsorm.MorphToMany{Related: &morphTag{}, Name: "taggable"},
	}
}

type morphTag struct {
	ID    uint
	Name  string
	Posts []*morphPost `gorm:"-"`
}

func (morphTag) TableName() string { return "morph_tags" }

func (morphTag) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Posts": contractsorm.MorphedByMany{Related: &morphPost{}, Name: "taggable"},
	}
}

type morphBadKind struct{}

func (morphBadKind) TableName() string { return "morph_bad_kinds" }

func (morphBadKind) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"X": unknownRelation{},
	}
}

type morphMissingRelated struct{}

func (morphMissingRelated) TableName() string { return "morph_missing_relateds" }

func (morphMissingRelated) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"X": contractsorm.MorphMany{Name: "imageable"},
	}
}

// --- Morph relation resolution tests ---

func TestResolveRelation_MorphTo(t *testing.T) {
	desc, err := resolveRelation(&morphImage{}, "Imageable")
	assert.NoError(t, err)
	assert.Equal(t, relKindMorphTo, desc.kind)
	assert.Equal(t, "imageable_type", desc.morphTypeColumn)
	assert.Equal(t, "imageable_id", desc.morphIDColumn)
	assert.Equal(t, "id", desc.morphOwnerKey)
	// MorphTo has no single related model; it's resolved per-row.
	assert.Nil(t, desc.relatedModel)
}

func TestResolveRelation_MorphToMany(t *testing.T) {
	desc, err := resolveRelation(&morphPost{}, "Tags")
	assert.NoError(t, err)
	assert.Equal(t, relKindMorphToMany, desc.kind)
	assert.Equal(t, "taggables", desc.pivotTable)
	assert.Equal(t, "taggable_type", desc.morphTypeColumn)
	assert.Equal(t, "taggable_id", desc.morphIDColumn)
	assert.False(t, desc.morphInverse)
	// morphValue defaults to the parent's table name when no MorphClass / morph map override.
	assert.Equal(t, "morph_posts", desc.morphValue)
}

func TestResolveRelation_MorphedByMany(t *testing.T) {
	desc, err := resolveRelation(&morphTag{}, "Posts")
	assert.NoError(t, err)
	assert.Equal(t, relKindMorphToMany, desc.kind)
	assert.True(t, desc.morphInverse)
	// For inverse, the morph value pins on the related's morph value.
	assert.Equal(t, "morph_posts", desc.morphValue)
}

func TestResolveRelation_MorphBadKind(t *testing.T) {
	_, err := resolveRelation(&morphBadKind{}, "X")
	assert.True(t, errors.Is(err, errors.OrmMorphRelationKindUnknown))
}

func TestResolveRelation_MorphMissingRelated(t *testing.T) {
	_, err := resolveRelation(&morphMissingRelated{}, "X")
	assert.True(t, errors.Is(err, errors.OrmMorphRelationMissingField))
}

// --- Forbidden GORM relation tags ---

type forbiddenPolymorphicParent struct {
	ID     uint
	Houses []*forbiddenPolymorphicChild `gorm:"polymorphic:Houseable"`
}

func (forbiddenPolymorphicParent) TableName() string { return "forbidden_polymorphic_parents" }

type forbiddenPolymorphicChild struct {
	ID            uint
	HouseableID   uint
	HouseableType string
}

func (forbiddenPolymorphicChild) TableName() string { return "forbidden_polymorphic_children" }

type forbiddenForeignKeyParent struct {
	ID    uint
	Books []*forbiddenForeignKeyChild `gorm:"foreignKey:ParentID"`
}

func (forbiddenForeignKeyParent) TableName() string { return "forbidden_foreign_key_parents" }

type forbiddenForeignKeyChild struct {
	ID       uint
	ParentID uint
}

func (forbiddenForeignKeyChild) TableName() string { return "forbidden_foreign_key_children" }

type forbiddenMany2ManyParent struct {
	ID    uint
	Roles []*forbiddenMany2ManyChild `gorm:"many2many:parent_roles"`
}

func (forbiddenMany2ManyParent) TableName() string { return "forbidden_many_2_many_parents" }

type forbiddenMany2ManyChild struct {
	ID uint
}

func (forbiddenMany2ManyChild) TableName() string { return "forbidden_many_2_many_children" }

func TestResolveRelation_ForbidsPolymorphicTag(t *testing.T) {
	_, err := resolveRelation(&forbiddenPolymorphicParent{}, "Houses")
	assert.True(t, errors.Is(err, errors.OrmRelationTagForbidden))
}

func TestResolveRelation_ForbidsForeignKeyTag(t *testing.T) {
	_, err := resolveRelation(&forbiddenForeignKeyParent{}, "Books")
	assert.True(t, errors.Is(err, errors.OrmRelationTagForbidden))
}

func TestResolveRelation_ForbidsMany2ManyTag(t *testing.T) {
	_, err := resolveRelation(&forbiddenMany2ManyParent{}, "Roles")
	assert.True(t, errors.Is(err, errors.OrmRelationTagForbidden))
}
