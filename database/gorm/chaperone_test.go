package gorm

import (
	"database/sql/driver"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/database/gorm/relations"
	"github.com/gradii/framework/errors"
)

// These tests cover the inverse-relation ("chaperone") write-back: a HasOne / HasMany / MorphOne /
// MorphMany declaring Inverse names a relation on the related model, and every relation read —
// the loaders' loadHasOneOrMany / loadMorph and the raw relation query (Related(...).Get / First,
// the after-query callback chaperone() registers; see applyInverseChaperone) — stores the matched
// parent instance on every related model it returns; HasOne::newRelatedInstanceFor applies it to
// a default instance too (Eloquent's SupportsInverseRelations, where matchOneOrMany applies
// applyInverseRelationToModel / ...ToCollection once the relation query has run). The loaders are driven against
// the recording driver so the identity assertion is on the same instances the loader saw; the
// end-to-end SQLite behavior (lazy Load, eager With, the relation-query reads, morph and
// one-of-many variants) is pinned in tests/database/orm_relation/inverse_relation and its sibling
// packages.

// chaperoneUser declares the corpus shape: a has-many and a has-one naming the child's User
// relation as the inverse, plus the controls — a plain has-many with no inverse and a has-one
// whose WithDefault instance must receive the inverse too.
type chaperoneUser struct {
	ID         uint
	Books      []*chaperoneBook `gorm:"-"`
	Book       *chaperoneBook   `gorm:"-"`
	PlainBooks []*chaperoneBook `gorm:"-"`
	NewBook    *chaperoneBook   `gorm:"-"`
}

func (chaperoneUser) TableName() string { return "chaperone_users" }

func (chaperoneUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Books":      contractsorm.HasMany{Related: &chaperoneBook{}, ForeignKey: "user_id", Inverse: "User"},
		"Book":       contractsorm.HasOne{Related: &chaperoneBook{}, ForeignKey: "user_id", Inverse: "User"},
		"PlainBooks": contractsorm.HasMany{Related: &chaperoneBook{}, ForeignKey: "user_id"},
		"NewBook":    contractsorm.HasOne{Related: &chaperoneBook{}, ForeignKey: "user_id", Inverse: "User", WithDefault: true},
	}
}

type chaperoneBook struct {
	ID     uint
	UserID uint
	User   *chaperoneUser `gorm:"-"`
}

func (chaperoneBook) TableName() string { return "chaperone_books" }

func (chaperoneBook) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"User": contractsorm.BelongsTo{Related: &chaperoneUser{}, ForeignKey: "user_id"},
	}
}

// TestLoadHasOneOrMany_ChaperoneStoresTheParentInstance drives loadHasOneOrMany with a canned
// child result set: every loaded child must point at the very parent struct the loader was given —
// Eloquent's assertSame($parent, $child->user) — and the write-back runs no query of its own.
func TestLoadHasOneOrMany_ChaperoneStoresTheParentInstance(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &chaperoneUser{})
	desc, err := relations.ResolveRelation(&chaperoneUser{}, "Books")
	require.NoError(t, err)
	assert.Equal(t, "User", desc.Inverse)

	recorder.returnRows("chaperone_books", []string{"id", "user_id"},
		[]driver.Value{int64(1), int64(7)},
		[]driver.Value{int64(2), int64(7)},
	)

	user := chaperoneUser{ID: 7}
	parents := []reflect.Value{reflect.ValueOf(&user).Elem()}

	require.NoError(t, q.loadHasOneOrMany(parents, &chaperoneUser{}, desc, eagerLoadEntry{Relation: "Books"}, nil, true))

	require.Len(t, user.Books, 2)
	for _, book := range user.Books {
		require.NotNil(t, book.User, "the inverse declaration must link every matched child")
		assert.Same(t, &user, book.User)
	}
	assert.Equal(t, 1, recorder.count(), "the chaperone adds no query")
}

// TestLoadHasOneOrMany_ChaperoneCapsToOneForHasOne: a HasOne destination takes the first match,
// and only that child is linked — matchOneOrMany's to-one truncation.
func TestLoadHasOneOrMany_ChaperoneCapsToOneForHasOne(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &chaperoneUser{})
	desc, err := relations.ResolveRelation(&chaperoneUser{}, "Book")
	require.NoError(t, err)

	recorder.returnRows("chaperone_books", []string{"id", "user_id"},
		[]driver.Value{int64(1), int64(7)},
		[]driver.Value{int64(2), int64(7)},
	)

	user := chaperoneUser{ID: 7}
	parents := []reflect.Value{reflect.ValueOf(&user).Elem()}

	require.NoError(t, q.loadHasOneOrMany(parents, &chaperoneUser{}, desc, eagerLoadEntry{Relation: "Book"}, nil, false))

	require.NotNil(t, user.Book)
	assert.Same(t, &user, user.Book.User)
}

// TestLoadHasOneOrMany_WithoutInverseDeclaration: the Go form of the cleared withoutInverse()
// state — a relation declared without Inverse — leaves every matched child untouched.
func TestLoadHasOneOrMany_WithoutInverseDeclaration(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &chaperoneUser{})
	desc, err := relations.ResolveRelation(&chaperoneUser{}, "PlainBooks")
	require.NoError(t, err)
	assert.Equal(t, "", desc.Inverse)

	recorder.returnRows("chaperone_books", []string{"id", "user_id"},
		[]driver.Value{int64(1), int64(7)},
		[]driver.Value{int64(2), int64(7)},
	)

	user := chaperoneUser{ID: 7}
	parents := []reflect.Value{reflect.ValueOf(&user).Elem()}

	require.NoError(t, q.loadHasOneOrMany(parents, &chaperoneUser{}, desc, eagerLoadEntry{Relation: "PlainBooks"}, nil, true))

	require.Len(t, user.PlainBooks, 2)
	for _, book := range user.PlainBooks {
		assert.Nil(t, book.User)
	}
}

// TestLoadHasOneOrMany_ChaperoneAppliesToWithDefaultInstance: a read that finds no row
// materialises the WithDefault instance through HasOne::newRelatedInstanceFor, which applies the
// inverse to that instance as well.
func TestLoadHasOneOrMany_ChaperoneAppliesToWithDefaultInstance(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &chaperoneUser{})
	desc, err := relations.ResolveRelation(&chaperoneUser{}, "NewBook")
	require.NoError(t, err)

	// An empty result set (distinct from no result set at all), so the default path runs.
	recorder.returnRows("chaperone_books", []string{"id", "user_id"})

	user := chaperoneUser{ID: 7}
	parents := []reflect.Value{reflect.ValueOf(&user).Elem()}

	require.NoError(t, q.loadHasOneOrMany(parents, &chaperoneUser{}, desc, eagerLoadEntry{Relation: "NewBook"}, nil, false))

	require.NotNil(t, user.NewBook, "WithDefault must seed the unmatched parent")
	assert.Zero(t, user.NewBook.ID, "the default is a fresh instance")
	assert.Equal(t, uint(7), user.NewBook.UserID, "the default carries the parent's local key")
	require.NotNil(t, user.NewBook.User)
	assert.Same(t, &user, user.NewBook.User, "the default instance carries the inverse too")
}

// chaperoneBadUser declares an Inverse naming a relation the related model does not have.
type chaperoneBadUser struct {
	ID    uint
	Posts []*chaperoneBook `gorm:"-"`
}

func (chaperoneBadUser) TableName() string { return "chaperone_bad_users" }

func (chaperoneBadUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Posts": contractsorm.HasMany{Related: &chaperoneBook{}, ForeignKey: "user_id", Inverse: "Nope"},
	}
}

// TestResolveRelation_InverseRelationMustBeDeclared: the declaration is validated against the
// related model's Relations() map at resolution — the point where Eloquent's chaperone() runs
// Model::isRelation() and throws RelationNotFoundException — so a relation naming an undeclared
// inverse fails with OrmRelationNotFound instead of attaching silently later.
func TestResolveRelation_InverseRelationMustBeDeclared(t *testing.T) {
	_, err := relations.ResolveRelation(&chaperoneBadUser{}, "Posts")

	require.Error(t, err)
	assert.ErrorIs(t, err, errors.OrmRelationNotFound)
	assert.Contains(t, err.Error(), `relation "Nope" not found on model`)
}

// --- relation read path (Related) --------------------------------------------
//
// Eloquent's chaperone() registers an after-query callback, so the write-back runs on every
// execution of the relation query — not only the loader's With / Load. The tests below drive
// Related(...) against the recording driver and pin the shapes the oracle measured
// (/tmp/verify-inverse-probe/direct_read.php and widened.php): get(), first(), and the widened
// orWhere read whose result includes rows whose foreign key belongs to another parent — every
// returned model still points at the parent the relation was resolved from. The write-back is
// in-memory and adds no query.

// TestRelated_ChaperoneLinksEveryModelInTheResult drives a widened relation-query read
// (Related(...).OrWhere(...).Get) whose result includes a row whose foreign key is another
// parent's: every returned child must point at the very parent the relation was resolved from
// (oracle: all 5 rows of the widened result link to the parent, the other parent's rows included),
// and the write-back runs no query of its own.
func TestRelated_ChaperoneLinksEveryModelInTheResult(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &chaperoneUser{})
	recorder.returnRows("chaperone_books", []string{"id", "user_id"},
		[]driver.Value{int64(1), int64(7)},
		[]driver.Value{int64(2), int64(7)},
		[]driver.Value{int64(3), int64(8)},
	)

	user := chaperoneUser{ID: 7}
	books := []*chaperoneBook{}
	require.NoError(t, q.Related(&user, "Books").OrWhere("user_id", 8).Get(&books))

	require.Len(t, books, 3)
	for _, book := range books {
		require.NotNil(t, book.User, "every model of the result must carry the parent as its inverse relation")
		assert.Same(t, &user, book.User)
	}
	assert.Equal(t, 1, recorder.count(), "the chaperone adds no query")
}

// TestRelated_ChaperoneLinksTheFirstResult: the single-row read (Related(...).First) carries the
// same write-back — Builder::first() is limit(1)->get(), so the callback runs there too.
func TestRelated_ChaperoneLinksTheFirstResult(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &chaperoneUser{})
	recorder.returnRows("chaperone_books", []string{"id", "user_id"},
		[]driver.Value{int64(1), int64(7)},
	)

	user := chaperoneUser{ID: 7}
	book := chaperoneBook{}
	require.NoError(t, q.Related(&user, "Books").First(&book))

	require.NotNil(t, book.User)
	assert.Same(t, &user, book.User)
	assert.Equal(t, 1, recorder.count(), "the chaperone adds no query")
}

// TestRelated_WithoutInverseDeclaration: a relation that declares no Inverse — the Go form of the
// cleared withoutInverse() state — leaves a relation-query read untouched.
func TestRelated_WithoutInverseDeclaration(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &chaperoneUser{})
	recorder.returnRows("chaperone_books", []string{"id", "user_id"},
		[]driver.Value{int64(1), int64(7)},
	)

	user := chaperoneUser{ID: 7}
	book := chaperoneBook{}
	require.NoError(t, q.Related(&user, "PlainBooks").First(&book))

	assert.Nil(t, book.User)
	assert.Equal(t, 1, recorder.count())
}
