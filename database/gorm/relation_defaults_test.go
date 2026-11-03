package gorm

import (
	"database/sql"
	"database/sql/driver"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/database/gorm/relations"
	"github.com/gradii/framework/database/orm/morphmap"
)

// --- models ------------------------------------------------------------------

// wdOwner is the related model the default-building tests fill: a fresh instance of it is the
// BelongsTo / HasOne default, and its holder_id column receives the HasOne default's foreign key.
type wdOwner struct {
	ID       uint64 `gorm:"column:id;primaryKey;autoIncrement"`
	Name     string `gorm:"column:name"`
	HolderID uint64 `gorm:"column:holder_id"`
}

func (wdOwner) TableName() string { return "wd_owners" }

// wdHolder declares the relation under test: Owner carries the plain withDefault(true) form. The
// morph columns let the MorphTo default's class rule be exercised without the morph map — an
// empty type column resolves to this model's own type.
type wdHolder struct {
	ID        uint64   `gorm:"column:id;primaryKey;autoIncrement"`
	OwnerID   uint64   `gorm:"column:owner_id"`
	OwnerType string   `gorm:"column:owner_type"`
	Owner     *wdOwner `gorm:"-"`
}

func (wdHolder) TableName() string { return "wd_holders" }

func (wdHolder) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Owner": contractsorm.BelongsTo{Related: &wdOwner{}, ForeignKey: "owner_id", OwnerKey: "id", WithDefault: true},
	}
}

// wdBadHolder declares a WithDefault value outside the accepted shapes; resolution must report it.
type wdBadHolder struct {
	ID      uint64   `gorm:"column:id;primaryKey;autoIncrement"`
	OwnerID uint64   `gorm:"column:owner_id"`
	Owner   *wdOwner `gorm:"-"`
}

func (wdBadHolder) TableName() string { return "wd_bad_holders" }

func (wdBadHolder) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Owner": contractsorm.BelongsTo{Related: &wdOwner{}, ForeignKey: "owner_id", WithDefault: "not-a-default"},
	}
}

// --- validation --------------------------------------------------------------

func TestValidWithDefault(t *testing.T) {
	assert.True(t, relations.ValidWithDefault(nil))
	assert.True(t, relations.ValidWithDefault(true))
	assert.True(t, relations.ValidWithDefault(map[string]any{"name": "taylor"}))
	assert.True(t, relations.ValidWithDefault(func(related, parent any) any { return nil }))
	assert.False(t, relations.ValidWithDefault("taylor"))
	assert.False(t, relations.ValidWithDefault(1))
}

func TestResolveRelationRejectsInvalidWithDefault(t *testing.T) {
	_, err := relations.ResolveRelation(&wdBadHolder{}, "Owner")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `invalid withDefault value string for relation "Owner"`)

	desc, err := relations.ResolveRelation(&wdHolder{}, "Owner")
	require.NoError(t, err)
	assert.Equal(t, true, desc.WithDefault)
}

// --- buildRelationDefault ----------------------------------------------------

func TestBuildRelationDefaultBelongsTo(t *testing.T) {
	q := newRelQuery(t)
	parent := reflect.ValueOf(&wdHolder{ID: 9, OwnerID: 4}).Elem()
	desc := &relationDescriptor{
		Kind:         relKindBelongsTo,
		Name:         "Owner",
		ParentTable:  "wd_holders",
		RelatedTable: "wd_owners",
		RelatedModel: &wdOwner{},
		WithDefault:  true,
	}

	row, ok, err := q.buildRelationDefault(desc, parent, &wdHolder{})
	require.NoError(t, err)
	require.True(t, ok)
	owner, isOwner := row.Interface().(*wdOwner)
	require.True(t, isOwner)
	assert.Zero(t, owner.ID)
	assert.Empty(t, owner.Name, "a plain withDefault() carries no attributes")

	// false / an empty map are falsy — no default (oracle: withDefault([]) is null).
	descFalse := *desc
	descFalse.WithDefault = false
	_, ok, err = q.buildRelationDefault(&descFalse, parent, &wdHolder{})
	require.NoError(t, err)
	assert.False(t, ok)

	descEmpty := *desc
	descEmpty.WithDefault = map[string]any{}
	_, ok, err = q.buildRelationDefault(&descEmpty, parent, &wdHolder{})
	require.NoError(t, err)
	assert.False(t, ok)

	// The attribute map force-fills the instance (Laravel's forceFill).
	descMap := *desc
	descMap.WithDefault = map[string]any{"name": "taylor"}
	row, ok, err = q.buildRelationDefault(&descMap, parent, &wdHolder{})
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "taylor", row.Interface().(*wdOwner).Name)

	// The closure mutates the instance in place; its nil return keeps it, and it receives the
	// actual parent model.
	descClosure := *desc
	descClosure.WithDefault = func(related, parentModel any) any {
		assert.Equal(t, uint64(9), parentModel.(*wdHolder).ID)
		related.(*wdOwner).Name = "taylor"
		return nil
	}
	row, ok, err = q.buildRelationDefault(&descClosure, parent, &wdHolder{})
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "taylor", row.Interface().(*wdOwner).Name)

	// A non-nil return replaces the instance, mirroring `$callback(...) ?: $instance`.
	replacement := &wdOwner{ID: 42}
	descReplace := *desc
	descReplace.WithDefault = func(related, parentModel any) any { return replacement }
	row, _, err = q.buildRelationDefault(&descReplace, parent, &wdHolder{})
	require.NoError(t, err)
	assert.Same(t, replacement, row.Interface())
}

func TestBuildRelationDefaultHasOneSetsForeignKey(t *testing.T) {
	q := newRelQuery(t)
	parent := reflect.ValueOf(&wdHolder{ID: 9}).Elem()
	desc := &relationDescriptor{
		Kind:         relKindHasOne,
		Name:         "Owner",
		ParentTable:  "wd_holders",
		RelatedTable: "wd_owners",
		RelatedModel: &wdOwner{},
		References: []referenceKey{{
			PrimaryTable:  "wd_holders",
			PrimaryColumn: "id",
			ForeignTable:  "wd_owners",
			ForeignColumn: "holder_id",
		}},
		WithDefault: true,
	}

	row, ok, err := q.buildRelationDefault(desc, parent, &wdHolder{})
	require.NoError(t, err)
	require.True(t, ok)
	owner := row.Interface().(*wdOwner)
	assert.Equal(t, uint64(9), owner.HolderID, "the HasOne default pre-sets the foreign key from the parent's local key")
}

func TestBuildRelationDefaultMorphToUsesParentClassForEmptyType(t *testing.T) {
	q := newRelQuery(t)
	parent := reflect.ValueOf(&wdHolder{ID: 9}).Elem()
	desc := &relationDescriptor{
		Kind:            relKindMorphTo,
		Name:            "Owner",
		ParentTable:     "wd_holders",
		MorphTypeColumn: "owner_type",
		MorphIDColumn:   "owner_id",
		WithDefault:     true,
	}

	row, ok, err := q.buildRelationDefault(desc, parent, &wdHolder{})
	require.NoError(t, err)
	require.True(t, ok)
	_, isHolder := row.Interface().(*wdHolder)
	assert.True(t, isHolder, "an empty morph type resolves the default to the parent's own class")
}

// --- assignToParents merge ---------------------------------------------------

func TestAssignToParentsAppliesDefaultWhenUnmatched(t *testing.T) {
	q := newRelQuery(t)
	schema, err := parseSchema(&wdHolder{})
	require.NoError(t, err)
	ownerField := schema.FieldsByDBName["owner_id"]
	require.NotNil(t, ownerField)

	defaultFor := func(parent reflect.Value) ([]reflect.Value, error) {
		return []reflect.Value{reflect.ValueOf(&wdOwner{ID: 7})}, nil
	}

	// A key nothing answered gets the default (the eager load's initRelation seeding).
	unmatched := &wdHolder{ID: 1}
	require.NoError(t, q.assignToParents([]reflect.Value{reflect.ValueOf(unmatched).Elem()}, ownerField, "Owner", nil, false, defaultFor))
	require.NotNil(t, unmatched.Owner)
	assert.Equal(t, uint64(7), unmatched.Owner.ID)

	// An empty parent key also gets the default (getResults' null-key branch).
	empty := &wdHolder{}
	require.NoError(t, q.assignToParents([]reflect.Value{reflect.ValueOf(empty).Elem()}, ownerField, "Owner", nil, false, defaultFor))
	require.NotNil(t, empty.Owner)

	// An answered foreign key keeps the row — the existing row wins over the default. The
	// dictionary is keyed by the related owner key and looked up through the parent's foreign key,
	// exactly as loadBelongsTo builds it.
	matched := &wdHolder{OwnerID: 3}
	dict := map[string][]reflect.Value{"3": {reflect.ValueOf(&wdOwner{ID: 5})}}
	require.NoError(t, q.assignToParents([]reflect.Value{reflect.ValueOf(matched).Elem()}, ownerField, "Owner", dict, false, defaultFor))
	require.NotNil(t, matched.Owner)
	assert.Equal(t, uint64(5), matched.Owner.ID)
}

// --- GAP A: PHP-falsy callback returns keep the fresh instance --------------------------------

// TestBuildRelationDefaultClosureFalsyKeepsInstance pins Laravel's `?:` against the probe set
// (/tmp/wd_edge_probe.php against illuminate/database v12.55.1: false / 0 / "" / [] / null / "0"
// all report result-is-related => true): a PHP-falsy return keeps the fresh instance — the
// callback's own mutations survive — while a PHP-truthy return installs the returned value.
func TestBuildRelationDefaultClosureFalsyKeepsInstance(t *testing.T) {
	q := newRelQuery(t)
	parent := reflect.ValueOf(&wdHolder{ID: 9, OwnerID: 4}).Elem()
	base := func() *relationDescriptor {
		return &relationDescriptor{
			Kind:         relKindBelongsTo,
			Name:         "Owner",
			ParentTable:  "wd_holders",
			RelatedTable: "wd_owners",
			RelatedModel: &wdOwner{},
		}
	}

	falsy := []struct {
		name  string
		value any
	}{
		{"false", false},
		{"zero-int", 0},
		{"zero-float", 0.0},
		{"empty-string", ""},
		{"string-zero", "0"},
		{"empty-slice", []any{}},
		{"empty-map", map[string]any{}},
		{"nil", nil},
		{"typed-nil-pointer", (*wdOwner)(nil)},
	}
	for _, tc := range falsy {
		t.Run(tc.name, func(t *testing.T) {
			desc := base()
			desc.WithDefault = func(related, parentModel any) any {
				related.(*wdOwner).Name = "taylor"
				return tc.value
			}
			row, ok, err := q.buildRelationDefault(desc, parent, &wdHolder{})
			require.NoError(t, err)
			require.True(t, ok)
			owner, isOwner := row.Interface().(*wdOwner)
			require.True(t, isOwner)
			assert.Equal(t, "taylor", owner.Name, "a PHP-falsy return must keep the mutated fresh instance")
		})
	}

	// Truthy returns install the returned value — a model, or (as PHP's dynamic `?:` would hand
	// back) any other truthy value.
	desc := base()
	replacement := &wdOwner{ID: 42}
	desc.WithDefault = func(related, parentModel any) any { return replacement }
	row, ok, err := q.buildRelationDefault(desc, parent, &wdHolder{})
	require.NoError(t, err)
	require.True(t, ok)
	assert.Same(t, replacement, row.Interface())

	descString := base()
	descString.WithDefault = func(related, parentModel any) any { return "taylor" }
	row, ok, err = q.buildRelationDefault(descString, parent, &wdHolder{})
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "taylor", row.Interface())
}

// TestRelationDefaultForFalsyClosureSeedsInsteadOfAssignError closes the refutation's failing
// path: a callback returning false used to surface as OrmEagerLoadCannotAssign when Load / With
// assigned the eager rows; with `?:` mirrored, the unmatched parent receives the fresh instance.
func TestRelationDefaultForFalsyClosureSeedsInsteadOfAssignError(t *testing.T) {
	q := newRelQuery(t)
	desc := &relationDescriptor{
		Kind:         relKindBelongsTo,
		Name:         "Owner",
		ParentTable:  "wd_holders",
		RelatedTable: "wd_owners",
		RelatedModel: &wdOwner{},
		WithDefault:  func(related, parent any) any { return false },
	}
	defaultFor := q.relationDefaultFor(desc, &wdHolder{})

	schema, err := parseSchema(&wdHolder{})
	require.NoError(t, err)
	ownerField := schema.FieldsByDBName["owner_id"]
	require.NotNil(t, ownerField)

	parent := &wdHolder{ID: 1}
	require.NoError(t, q.assignToParents([]reflect.Value{reflect.ValueOf(parent).Elem()}, ownerField, "Owner", nil, false, defaultFor))
	require.NotNil(t, parent.Owner, "the unmatched parent must receive the fresh default, not an assign error")
	assert.Zero(t, parent.Owner.ID)
}

// --- GAP B: MorphOne / HasOneThrough parity ---------------------------------------------------

// wdImage is the MorphOne related model: the default instance's pre-set polymorphic id and type
// columns land on owner_id / owner_type. Its Holder relation backs the MorphOne's declared
// inverse, so the default instance's chaperone write-back can be asserted.
type wdImage struct {
	ID        uint64         `gorm:"column:id;primaryKey;autoIncrement"`
	OwnerID   uint64         `gorm:"column:owner_id"`
	OwnerType string         `gorm:"column:owner_type"`
	Holder    *wdMorphHolder `gorm:"-"`
}

func (wdImage) TableName() string { return "wd_images" }

func (wdImage) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Holder": contractsorm.BelongsTo{Related: &wdMorphHolder{}, ForeignKey: "owner_id", OwnerKey: "id"},
	}
}

// wdMorphHolder declares the MorphOne under test: Name "owner" gives owner_type / owner_id, and
// Inverse carries the chaperone.
type wdMorphHolder struct {
	ID    uint64   `gorm:"column:id;primaryKey;autoIncrement"`
	Image *wdImage `gorm:"-"`
}

func (wdMorphHolder) TableName() string { return "wd_morph_holders" }

func (wdMorphHolder) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Image": contractsorm.MorphOne{Related: &wdImage{}, Name: "owner", Inverse: "Holder", WithDefault: true},
	}
}

// wdCountry / wdUser / wdArticle model the through chain the HasOneThrough default follows:
// wdArticle is reached from wdCountry through wdUser.
type wdCountry struct {
	ID      uint64     `gorm:"column:id;primaryKey;autoIncrement"`
	Article *wdArticle `gorm:"-"`
}

func (wdCountry) TableName() string { return "wd_countries" }

func (wdCountry) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Article": contractsorm.HasOneThrough{
			Related:     &wdArticle{},
			Through:     &wdUser{},
			FirstKey:    "country_id",
			SecondKey:   "user_id",
			WithDefault: true,
		},
	}
}

type wdUser struct {
	ID        uint64 `gorm:"column:id;primaryKey;autoIncrement"`
	CountryID uint64 `gorm:"column:country_id"`
}

func (wdUser) TableName() string { return "wd_users" }

type wdArticle struct {
	ID     uint64 `gorm:"column:id;primaryKey;autoIncrement"`
	UserID uint64 `gorm:"column:user_id"`
}

func (wdArticle) TableName() string { return "wd_articles" }

// wdBadMorphHolder / wdBadThroughHolder declare WithDefault outside the accepted shapes; both
// kinds must report OrmRelationWithDefaultInvalid at resolution, like HasOne / BelongsTo.
type wdBadMorphHolder struct {
	ID uint64 `gorm:"column:id;primaryKey;autoIncrement"`
}

func (wdBadMorphHolder) TableName() string { return "wd_bad_morph_holders" }

func (wdBadMorphHolder) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Image": contractsorm.MorphOne{Related: &wdImage{}, Name: "owner", WithDefault: "not-a-default"},
	}
}

type wdBadThroughHolder struct {
	ID uint64 `gorm:"column:id;primaryKey;autoIncrement"`
}

func (wdBadThroughHolder) TableName() string { return "wd_bad_through_holders" }

func (wdBadThroughHolder) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Article": contractsorm.HasOneThrough{Related: &wdArticle{}, Through: &wdUser{}, FirstKey: "country_id", SecondKey: "user_id", WithDefault: "not-a-default"},
	}
}

func TestResolveRelationCarriesWithDefaultForMorphOneAndHasOneThrough(t *testing.T) {
	morphDesc, err := relations.ResolveRelation(&wdMorphHolder{}, "Image")
	require.NoError(t, err)
	assert.Equal(t, true, morphDesc.WithDefault)

	throughDesc, err := relations.ResolveRelation(&wdCountry{}, "Article")
	require.NoError(t, err)
	assert.Equal(t, true, throughDesc.WithDefault)
}

func TestResolveRelationRejectsInvalidWithDefaultOnMorphOneAndHasOneThrough(t *testing.T) {
	_, err := relations.ResolveRelation(&wdBadMorphHolder{}, "Image")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `invalid withDefault value string for relation "Image"`)

	_, err = relations.ResolveRelation(&wdBadThroughHolder{}, "Article")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `invalid withDefault value string for relation "Article"`)
}

func TestBuildRelationDefaultMorphOneSetsForeignKeyAndType(t *testing.T) {
	q := newRelQuery(t)
	desc, err := relations.ResolveRelation(&wdMorphHolder{}, "Image")
	require.NoError(t, err)

	parent := reflect.ValueOf(&wdMorphHolder{ID: 9}).Elem()
	row, ok, err := q.buildRelationDefault(desc, parent, &wdMorphHolder{ID: 9})
	require.NoError(t, err)
	require.True(t, ok)
	image, isImage := row.Interface().(*wdImage)
	require.True(t, isImage)
	assert.Equal(t, uint64(9), image.OwnerID, "the MorphOne default pre-sets the polymorphic id from the parent's local key")
	assert.Equal(t, desc.MorphValue, image.OwnerType, "the MorphOne default pre-sets the type column to the relation's morph value")
	require.NotNil(t, image.Holder, "the inverse relation is applied to the default instance")
	assert.Equal(t, uint64(9), image.Holder.ID)

	// false / an empty map are falsy — no default (withDefault([]) is null).
	descFalse := *desc
	descFalse.WithDefault = false
	_, ok, err = q.buildRelationDefault(&descFalse, parent, &wdMorphHolder{})
	require.NoError(t, err)
	assert.False(t, ok)

	descEmpty := *desc
	descEmpty.WithDefault = map[string]any{}
	_, ok, err = q.buildRelationDefault(&descEmpty, parent, &wdMorphHolder{})
	require.NoError(t, err)
	assert.False(t, ok)

	// The map form force-fills the instance on top of the pre-set id / type.
	descMap := *desc
	descMap.WithDefault = map[string]any{"id": uint64(11)}
	row, ok, err = q.buildRelationDefault(&descMap, parent, &wdMorphHolder{})
	require.NoError(t, err)
	require.True(t, ok)
	image = row.Interface().(*wdImage)
	assert.Equal(t, uint64(11), image.ID)
	assert.Equal(t, uint64(9), image.OwnerID)
	assert.Equal(t, desc.MorphValue, image.OwnerType)
}

func TestBuildRelationDefaultHasOneThroughPlainInstance(t *testing.T) {
	q := newRelQuery(t)
	desc, err := relations.ResolveRelation(&wdCountry{}, "Article")
	require.NoError(t, err)

	parent := reflect.ValueOf(&wdCountry{ID: 3}).Elem()
	row, ok, err := q.buildRelationDefault(desc, parent, &wdCountry{ID: 3})
	require.NoError(t, err)
	require.True(t, ok)
	article, isArticle := row.Interface().(*wdArticle)
	require.True(t, isArticle)
	assert.Zero(t, article.ID)
	assert.Zero(t, article.UserID, "a HasOneThrough default is a plain newInstance — no attributes")

	// false / an empty map still disable it.
	descFalse := *desc
	descFalse.WithDefault = false
	_, ok, err = q.buildRelationDefault(&descFalse, parent, &wdCountry{})
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestAssignToParentsSeedsMorphOneDefault(t *testing.T) {
	q := newRelQuery(t)
	desc, err := relations.ResolveRelation(&wdMorphHolder{}, "Image")
	require.NoError(t, err)
	defaultFor := q.relationDefaultFor(desc, &wdMorphHolder{})

	schema, err := parseSchema(&wdMorphHolder{})
	require.NoError(t, err)
	idField := schema.FieldsByDBName["id"]
	require.NotNil(t, idField)

	// A parent whose key answered no row takes the default (the eager load's initRelation seeding).
	unmatched := &wdMorphHolder{ID: 7}
	require.NoError(t, q.assignToParents([]reflect.Value{reflect.ValueOf(unmatched).Elem()}, idField, "Image", nil, false, defaultFor))
	require.NotNil(t, unmatched.Image)
	assert.Equal(t, uint64(7), unmatched.Image.OwnerID)
	assert.Equal(t, desc.MorphValue, unmatched.Image.OwnerType)

	// An answered key keeps the row — the existing row wins over the default.
	matched := &wdMorphHolder{ID: 8}
	dict := map[string][]reflect.Value{"8": {reflect.ValueOf(&wdImage{ID: 5, OwnerID: 8, OwnerType: desc.MorphValue})}}
	require.NoError(t, q.assignToParents([]reflect.Value{reflect.ValueOf(matched).Elem()}, idField, "Image", dict, false, defaultFor))
	require.NotNil(t, matched.Image)
	assert.Equal(t, uint64(5), matched.Image.ID)
}

func TestAssignToParentsSeedsHasOneThroughDefault(t *testing.T) {
	q := newRelQuery(t)
	desc, err := relations.ResolveRelation(&wdCountry{}, "Article")
	require.NoError(t, err)
	defaultFor := q.relationDefaultFor(desc, &wdCountry{})

	schema, err := parseSchema(&wdCountry{})
	require.NoError(t, err)
	idField := schema.FieldsByDBName["id"]
	require.NotNil(t, idField)

	unmatched := &wdCountry{ID: 7}
	require.NoError(t, q.assignToParents([]reflect.Value{reflect.ValueOf(unmatched).Elem()}, idField, "Article", nil, false, defaultFor))
	require.NotNil(t, unmatched.Article)
	assert.Zero(t, unmatched.Article.ID)

	matched := &wdCountry{ID: 8}
	dict := map[string][]reflect.Value{"8": {reflect.ValueOf(&wdArticle{ID: 5, UserID: 10})}}
	require.NoError(t, q.assignToParents([]reflect.Value{reflect.ValueOf(matched).Elem()}, idField, "Article", dict, false, defaultFor))
	require.NotNil(t, matched.Article)
	assert.Equal(t, uint64(5), matched.Article.ID)
}

// --- pointer-typed morph type column in defaults ----------------------------------------------

// A MorphTo default resolves its class off the parent's morph type column (see
// newMorphToDefaultInstance), so a nullable morph_type column — a *string, or a driver.Valuer
// wrapper like *sql.NullString — must reach the morph map as the value the field carries. These
// fixtures mirror the pointer-key tests: both morph halves are pointers and the MorphTo carries
// WithDefault.

// wdPtrMorphRelated is the morph target registered under the alias the type columns below carry.
type wdPtrMorphRelated struct {
	ID uint64 `gorm:"column:id;primaryKey;autoIncrement"`
}

func (wdPtrMorphRelated) TableName() string { return "wd_ptr_morph_relateds" }

// wdPtrMorphHolder holds the morph type as *string — the Go shape of a nullable morph_type
// column — and declares the WithDefault MorphTo.
type wdPtrMorphHolder struct {
	ID        uint64  `gorm:"column:id;primaryKey;autoIncrement"`
	OwnerID   *uint64 `gorm:"column:owner_id"`
	OwnerType *string `gorm:"column:owner_type"`
	Owner     any     `gorm:"-"`
}

func (wdPtrMorphHolder) TableName() string { return "wd_ptr_morph_holders" }

func (wdPtrMorphHolder) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Owner": contractsorm.MorphTo{Name: "owner", TypeColumn: "owner_type", IDColumn: "owner_id", WithDefault: true},
	}
}

// wdPtrMorphNullHolder is the driver.Valuer shape of the same column: the alias lives in the
// wrapper's Value(), never in its fields.
type wdPtrMorphNullHolder struct {
	ID        uint64          `gorm:"column:id;primaryKey;autoIncrement"`
	OwnerID   *uint64         `gorm:"column:owner_id"`
	OwnerType *sql.NullString `gorm:"column:owner_type"`
	Owner     any             `gorm:"-"`
}

func (wdPtrMorphNullHolder) TableName() string { return "wd_ptr_morph_null_holders" }

// TestBuildRelationDefaultMorphToPointerTypeColumnResolvesClass is the regression for the third
// deref site: newMorphToDefaultInstance reads the parent's morph type column and resolves it
// through the morph map, exactly as loadMorphTo already dereferences. With the type behind a
// *string — or a driver.Valuer wrapper — the raw field stringified to the pointer address (or
// the wrapper's "{alias true}" fields), no registration matched, and the build failed with
// OrmMorphTypeUnknown. The morph id is empty throughout: the default's class comes from the type
// column alone, the empty-id branch getResults takes before any related read.
func TestBuildRelationDefaultMorphToPointerTypeColumnResolvesClass(t *testing.T) {
	morphmap.Reset()
	defer morphmap.Reset()
	morphmap.Register(map[string]any{"wd_ptr_morph": &wdPtrMorphRelated{}})

	q := newRelQuery(t)
	alias := "wd_ptr_morph"
	desc := &relationDescriptor{
		Kind:            relKindMorphTo,
		Name:            "Owner",
		ParentTable:     "wd_ptr_morph_holders",
		MorphTypeColumn: "owner_type",
		MorphIDColumn:   "owner_id",
		WithDefault:     true,
	}

	holder := &wdPtrMorphHolder{OwnerType: &alias}
	row, ok, err := q.buildRelationDefault(desc, reflect.ValueOf(holder).Elem(), holder)
	require.NoError(t, err)
	require.True(t, ok)
	related, isRelated := row.Interface().(*wdPtrMorphRelated)
	require.True(t, isRelated, "a registered alias behind a *string must resolve its class")
	assert.Zero(t, related.ID)

	nullHolder := &wdPtrMorphNullHolder{OwnerType: &sql.NullString{String: alias, Valid: true}}
	row, ok, err = q.buildRelationDefault(desc, reflect.ValueOf(nullHolder).Elem(), nullHolder)
	require.NoError(t, err)
	require.True(t, ok)
	_, isRelated = row.Interface().(*wdPtrMorphRelated)
	require.True(t, isRelated, "a registered alias behind a driver.Valuer must resolve its class")

	// A nil pointer and an invalid wrapper are the empty type column: the parent's own class, not
	// an unknown-type error.
	empty := &wdPtrMorphHolder{}
	row, ok, err = q.buildRelationDefault(desc, reflect.ValueOf(empty).Elem(), empty)
	require.NoError(t, err)
	require.True(t, ok)
	assert.IsType(t, empty, row.Interface(), "a nil pointer type must read as empty")

	emptyNull := &wdPtrMorphNullHolder{OwnerType: &sql.NullString{}}
	row, ok, err = q.buildRelationDefault(desc, reflect.ValueOf(emptyNull).Elem(), emptyNull)
	require.NoError(t, err)
	require.True(t, ok)
	assert.IsType(t, emptyNull, row.Interface(), "an invalid wrapper must read as empty")
}

// TestEagerWithMorphTo_PointerTypeColumnWithDefaultSeedsMissingRow drives the missing-row path
// end to end: the parent hydrates a pointer-typed morph type and id, the related read finds no
// row, and the eager seeding must build the default from the alias the type column carries.
// Before the fix the type read stringified the pointer, no registration matched, and the load
// failed with OrmMorphTypeUnknown instead of seeding the default.
func TestEagerWithMorphTo_PointerTypeColumnWithDefaultSeedsMissingRow(t *testing.T) {
	morphmap.Reset()
	defer morphmap.Reset()
	morphmap.Register(map[string]any{"wd_ptr_morph": &wdPtrMorphRelated{}})

	q, recorder := newRecordingRelQuery(t, &wdPtrMorphHolder{})
	q = q.With("Owner").(*OrmBuilder)

	recorder.returnRows(`from "wd_ptr_morph_holders"`, []string{"id", "owner_id", "owner_type"},
		[]driver.Value{int64(1), int64(5), "wd_ptr_morph"},
	)
	// The related read runs and finds no row — the default stands in.
	recorder.returnRows(`from "wd_ptr_morph_relateds"`, []string{"id"})

	var holders []wdPtrMorphHolder
	require.NoError(t, q.Get(&holders))
	require.Len(t, holders, 1)

	require.NotNil(t, holders[0].OwnerType, "the parent read must hydrate the pointer type column")
	assert.Equal(t, "wd_ptr_morph", *holders[0].OwnerType)
	require.NotNil(t, holders[0].OwnerID, "the parent read must hydrate the pointer id column")
	assert.Equal(t, uint64(5), *holders[0].OwnerID)

	// The related read carried the pointed-to id; the miss seeded the type column's class.
	require.Equal(t, 2, recorder.count())
	assert.Equal(t, `select * from "wd_ptr_morph_relateds" where "wd_ptr_morph_relateds"."id" in (?)`, recorder.last(t).sql)
	assert.Equal(t, []driver.Value{int64(5)}, recorder.last(t).args)

	require.NotNil(t, holders[0].Owner, "a missed read must seed the default class the pointer type addresses")
	related, ok := holders[0].Owner.(*wdPtrMorphRelated)
	require.True(t, ok)
	assert.Zero(t, related.ID)
}
