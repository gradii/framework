package gorm

import (
	"database/sql/driver"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
)

// This file pins the engine halves of item lalign-defaults-inverse: the call-time WithDefault slot
// (SupportsDefaultModels::withDefault, Relations/Concerns/SupportsDefaultModels.php:32) and the
// inverse write-back on the make/create paths (SupportsInverseRelations::applyInverseRelationToModel
// through HasOneOrMany::make :65, ::create :393 and the trailing call of
// setForeignAttributesForCreate :506). Every behavior was measured by the oracle against
// illuminate/database v12.55.1 (php .claude/laravel-oracle/oracle.php
// /tmp/lalign-defaults-inverse/runtime_withdefault.php and inverse_writeback_runtime.php):
// hasOne.missing-row.attributes {"foreign_key":2}, hasOne.null-key.queries 0,
// hasOne.declared-then-runtime.name "runtime", create/make.same-instance true,
// withoutChaperone-create/make.relationLoaded false, gir.after-withoutChaperone NULL. The
// projection-level half lives in database/gorm/relations/defaults_inverse_unit_test.go; the sqlite
// end-to-end half in tests/database/orm_relation/{has_one,inverse_relation_has_many}.

// rtdUser / rtdBook carry a declared default the call-time slot replaces — the one slot
// SupportsDefaultModels::$withDefault holds.
type rtdUser struct {
	ID   uint     `gorm:"column:id;primaryKey"`
	Book *rtdBook `gorm:"-"`
}

func (rtdUser) TableName() string { return "rtd_users" }

func (rtdUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Book": contractsorm.HasOne{
			Related: &rtdBook{}, ForeignKey: "user_id", Inverse: "User",
			WithDefault: map[string]any{"title": "declared"},
		},
	}
}

type rtdBook struct {
	ID     uint     `gorm:"column:id;primaryKey"`
	UserID uint     `gorm:"column:user_id"`
	Title  string   `gorm:"column:title"`
	User   *rtdUser `gorm:"-"`
}

func (rtdBook) TableName() string { return "rtd_books" }

func (rtdBook) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"User": contractsorm.BelongsTo{Related: &rtdUser{}, ForeignKey: "user_id"},
	}
}

// TestRelated_WithDefaultCallTimeReplacesTheDeclaredSlot drives the object-path default
// materialisation over the real builder: the declaration seeds the slot, the call-time WithDefault
// replaces it, the fresh instance carries the FK stamp and the inverse link, and a falsy slot
// yields no default at all.
func TestRelated_WithDefaultCallTimeReplacesTheDeclaredSlot(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &rtdUser{})

	// Declared only: the declaration's map rides the slot.
	user := rtdUser{ID: 7}
	rel := q.Related(&user, "Book").(contractsorm.HasOneRelation)
	var declaredBook rtdBook
	require.NoError(t, rel.GetResults(&declaredBook))
	assert.Equal(t, "declared", declaredBook.Title, "the declaration's value seeds the slot")

	// The call-time call replaces it (oracle: hasOne.declared-then-runtime.name "runtime").
	rel.WithDefault(map[string]any{"title": "runtime"})
	var runtimeBook rtdBook
	require.NoError(t, rel.GetResults(&runtimeBook))
	assert.Equal(t, "runtime", runtimeBook.Title)
	assert.Equal(t, uint(7), runtimeBook.UserID, "the fresh default carries the FK stamp (HasOne::newRelatedInstanceFor)")
	require.NotNil(t, runtimeBook.User)
	assert.Same(t, &user, runtimeBook.User, "the default instance receives the inverse relation")
	assert.Equal(t, 2, recorder.count(), "each miss runs its relation read; the default adds no query")

	// false yields no default — Eloquent's falsy `if (! $this->withDefault) return;` gate.
	rel.WithDefault(false)
	var noneBook rtdBook
	require.NoError(t, rel.GetResults(&noneBook))
	assert.Zero(t, noneBook.Title)
	assert.Zero(t, noneBook.UserID)
	assert.Nil(t, noneBook.User)
}

// TestRelated_WithDefaultEmptyMapAndClosureForms pins the remaining accepted forms on the
// call-time path: an empty map is falsy, a callback's falsy return keeps the fresh instance, and a
// truthy return replaces it.
func TestRelated_WithDefaultEmptyMapAndClosureForms(t *testing.T) {
	q, _ := newRecordingRelQuery(t, &rtdUser{})

	user := rtdUser{ID: 9}
	rel := q.Related(&user, "Book").(contractsorm.HasOneRelation)

	// An empty map is PHP-falsy (oracle: withDefault([]) is null).
	rel.WithDefault(map[string]any{})
	var empty rtdBook
	require.NoError(t, rel.GetResults(&empty))
	assert.Zero(t, empty.Title)

	// The closure receives the fresh instance and the parent; a falsy return keeps the instance
	// (Laravel's `call_user_func($callback, $instance, $parent) ?: $instance`).
	rel.WithDefault(func(related, parent any) any {
		book := related.(*rtdBook)
		book.Title = "closure"

		return nil
	})
	var closed rtdBook
	require.NoError(t, rel.GetResults(&closed))
	assert.Equal(t, "closure", closed.Title)

	// A truthy return replaces the instance.
	rel.WithDefault(func(related, parent any) any { return &rtdBook{Title: "replacement"} })
	var replaced rtdBook
	require.NoError(t, rel.GetResults(&replaced))
	assert.Equal(t, "replacement", replaced.Title)
}

// TestRelated_WithDefaultNullParentKeySkipsTheRead: HasOne::getResults materialises the default
// without running the relation read once the parent key is null (oracle: hasOne.null-key.queries 0).
func TestRelated_WithDefaultNullParentKeySkipsTheRead(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &rtdUser{})

	rel := q.Related(&rtdUser{}, "Book").(contractsorm.HasOneRelation)
	rel.WithDefault(map[string]any{"title": "runtime"})
	var book rtdBook
	require.NoError(t, rel.GetResults(&book))
	assert.Equal(t, "runtime", book.Title)
	assert.Zero(t, book.UserID, "a null parent key stamps no foreign key value")
	assert.Equal(t, 0, recorder.count(), "the absent parent key short-circuits the read")
}

// TestRelated_WithoutChaperoneClearsTheReadLink: withoutInverse / withoutChaperone clear the state
// the after-query write-back reads, so a relation-query read links nothing afterwards.
func TestRelated_WithoutChaperoneClearsTheReadLink(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &chaperoneUser{})
	recorder.returnRows("chaperone_books", []string{"id", "user_id"},
		[]driver.Value{int64(1), int64(7)},
	)

	user := chaperoneUser{ID: 7}
	rel := q.Related(&user, "Books").(contractsorm.HasOneOrManyRelation)
	assert.Equal(t, "User", rel.GetInverseRelationship(), "the declaration seeds the inverse state")

	// withoutInverse is the alias of withoutChaperone (SupportsInverseRelations.php:141/:151).
	rel.WithoutInverse()
	assert.Equal(t, "", rel.GetInverseRelationship(), "the cleared state reports NULL (oracle: gir.after-withoutInverse)")

	books := []*chaperoneBook{}
	require.NoError(t, rel.Get(&books))
	require.Len(t, books, 1)
	assert.Nil(t, books[0].User, "a cleared relation read links nothing (oracle: withoutInverse-get.any-relationLoaded false)")
}

// TestRelated_WriteBackLinksCreatedAndMadeChildren: make / create link the child back to the
// parent in memory (HasOneOrMany.php:65/:393/:506) with no extra statement.
func TestRelated_WriteBackLinksCreatedAndMadeChildren(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &chaperoneUser{})
	user := chaperoneUser{ID: 7}
	rel := q.Related(&user, "Books").(contractsorm.HasOneOrManyRelation)

	created := chaperoneBook{}
	require.NoError(t, rel.Create(&created))
	assert.Equal(t, uint(7), created.UserID, "the FK stamp still lands")
	assert.Equal(t, 1, recorder.count(), "the create runs its insert; the link adds no query")
	require.NotNil(t, created.User)
	assert.Same(t, &user, created.User, "create() links the child (oracle: 1 create.same-instance true)")

	made := chaperoneBook{}
	require.NoError(t, rel.Make(&made, nil))
	assert.Same(t, &user, made.User, "make() links the child (oracle: 2 make.same-instance true)")
	assert.Equal(t, 1, recorder.count(), "make persists nothing")
}

// TestRelated_WithoutChaperoneLeavesChildrenUnlinked: the cleared state reaches the write path —
// withoutChaperone()->create() / make() link nothing, the quiet variants included (the quiet
// session clone keeps the cleared state).
func TestRelated_WithoutChaperoneLeavesChildrenUnlinked(t *testing.T) {
	q, _ := newRecordingRelQuery(t, &chaperoneUser{})
	user := chaperoneUser{ID: 7}
	rel := q.Related(&user, "Books").(contractsorm.HasOneOrManyRelation)

	rel.WithoutChaperone()

	created := chaperoneBook{}
	require.NoError(t, rel.Create(&created))
	assert.Nil(t, created.User, "withoutChaperone()->create() links nothing (oracle: 6 relationLoaded false)")

	quiet := chaperoneBook{}
	require.NoError(t, rel.CreateQuietly(&quiet))
	assert.Nil(t, quiet.User, "the cleared state survives the quiet session clone")

	made := chaperoneBook{}
	require.NoError(t, rel.Make(&made, nil))
	assert.Nil(t, made.User, "withoutChaperone()->make() links nothing (oracle: 7 relationLoaded false)")
}

// TestRelated_FlatShellLinksThroughTheDeclaration: a write that carries no relation object — the
// flat RelationWriter surface — falls back to the declaration's Inverse, the same link the object
// path applies by default.
func TestRelated_FlatShellLinksThroughTheDeclaration(t *testing.T) {
	q, _ := newRecordingRelQuery(t, &chaperoneUser{})
	user := chaperoneUser{ID: 7}

	book := chaperoneBook{}
	require.NoError(t, q.CreateRelation(&user, "Books", &book))
	require.NotNil(t, book.User)
	assert.Same(t, &user, book.User)
}

// TestRelated_ManyVariantsLinkEveryChild: createMany / makeMany link every child (the corpus
// measured createMany.all-same / makeMany.all-same true).
func TestRelated_ManyVariantsLinkEveryChild(t *testing.T) {
	q, _ := newRecordingRelQuery(t, &chaperoneUser{})
	user := chaperoneUser{ID: 7}
	rel := q.Related(&user, "Books").(contractsorm.HasOneOrManyRelation)

	created := make([]chaperoneBook, 2)
	require.NoError(t, rel.CreateMany(&created))
	for i := range created {
		require.NotNil(t, created[i].User, "createMany links child %d", i)
		assert.Same(t, &user, created[i].User)
	}

	// MakeMany appends the built children to the destination slice, so it starts empty.
	made := make([]chaperoneBook, 0, 2)
	require.NoError(t, rel.MakeMany([]map[string]any{{}, {}}, &made))
	require.Len(t, made, 2)
	for i := range made {
		require.NotNil(t, made[i].User, "makeMany links child %d", i)
		assert.Same(t, &user, made[i].User)
	}
}

// TestRelated_WithoutChaperoneAlsoClearsTheDefaultLink: the cleared state reaches the default
// materialisation too — the fresh default instance carries the FK stamp but no inverse link.
// Eloquent's newRelatedInstanceFor applies applyInverseRelationToModel, which reads the live
// getInverseRelationship() — null after withoutChaperone / withoutInverse (oracle: probe_c.php
// E/F/H report linked false, where withoutChaperone()->withDefault(true)->getResults() carries
// the foreign key only).
func TestRelated_WithoutChaperoneAlsoClearsTheDefaultLink(t *testing.T) {
	q, _ := newRecordingRelQuery(t, &rtdUser{})

	user := rtdUser{ID: 7}

	callTime := q.Related(&user, "Book").(contractsorm.HasOneRelation)
	callTime.WithoutChaperone()
	callTime.WithDefault(map[string]any{"title": "runtime"})

	var book rtdBook
	require.NoError(t, callTime.GetResults(&book))
	assert.Equal(t, "runtime", book.Title)
	assert.Equal(t, uint(7), book.UserID, "the FK stamp survives the clear")
	assert.Nil(t, book.User, "the cleared state must reach the default instance link")

	declared := q.Related(&user, "Book").(contractsorm.HasOneRelation)
	declared.WithoutChaperone()

	var declaredBook rtdBook
	require.NoError(t, declared.GetResults(&declaredBook))
	assert.Equal(t, "declared", declaredBook.Title)
	assert.Nil(t, declaredBook.User, "the declared default links nothing once cleared either")
}
