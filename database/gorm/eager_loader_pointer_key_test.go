package gorm

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsdatabase "github.com/gradii/framework/contracts/database"
	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/database/orm/morphmap"
)

// Pointer-typed key fields are the Go shape of a nullable column: the FK lives behind a *uint64
// and the loader must key its dictionaries by the value the pointer addresses, never by the
// pointer itself (fmt.Sprint prints the address). The fixtures mirror the belongs_to corpus's
// parent/related pair with the FK as a pointer, so both read paths the defect touched — Load and
// eager With — run end to end through the recording driver.

// relPtrAuthor is the related row: an auto-incrementing id and a name.
type relPtrAuthor struct {
	ID   uint64 `gorm:"column:id;primaryKey;autoIncrement"`
	Name string `gorm:"column:name"`
}

func (relPtrAuthor) TableName() string { return "rel_ptr_authors" }

// relPtrBook holds the pointer-typed FKs: author_id belongs to relPtrAuthor and user_id belongs to
// relPtrUser, both nullable so the pointer is the only faithful Go shape.
type relPtrBook struct {
	ID       uint64        `gorm:"column:id;primaryKey;autoIncrement"`
	Title    string        `gorm:"column:title"`
	AuthorID *uint64       `gorm:"column:author_id"`
	UserID   *uint64       `gorm:"column:user_id"`
	Author   *relPtrAuthor `gorm:"-"`
}

func (relPtrBook) TableName() string { return "rel_ptr_books" }

func (relPtrBook) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Author": contractsorm.BelongsTo{Related: &relPtrAuthor{}, ForeignKey: "author_id", OwnerKey: "id"},
	}
}

// relPtrUser owns the books through relPtrBook.UserID, the has-many half of the pointer-key path.
type relPtrUser struct {
	ID    uint64        `gorm:"column:id;primaryKey;autoIncrement"`
	Books []*relPtrBook `gorm:"-"`
}

func (relPtrUser) TableName() string { return "rel_ptr_users" }

func (relPtrUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Books": contractsorm.HasMany{Related: &relPtrBook{}, ForeignKey: "user_id"},
	}
}

// TestLoadBelongsTo_PointerForeignKeyResolves is the regression for the pointer-FK loader defect:
// a belongs-to parent whose FK field is *uint64 and points at an existing row must resolve its
// relation. Before the fix the loader read the raw pointer from the schema field and dictKey
// stringified the address, so the dictionary lookup missed and Load left Author nil even though
// the FK was present. Laravel keys the relation dictionary by the FK value itself
// (BelongsTo::match -> getDictionaryKey); the Go keying must agree.
func TestLoadBelongsTo_PointerForeignKeyResolves(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &relPtrBook{})
	recorder.returnRows(`from "rel_ptr_authors"`, []string{"id", "name"},
		[]driver.Value{int64(1), "Ada"},
	)

	fk := uint64(1)
	book := &relPtrBook{ID: 7, Title: "first", AuthorID: &fk}

	require.NoError(t, q.Load(book, "Author"))

	// The related read carried the pointed-to key, not the pointer: database/sql's converter
	// dereferences pointers, and the statement is the one a scalar FK compiles to.
	assert.Equal(t, `select * from "rel_ptr_authors" where "rel_ptr_authors"."id" in (?)`, recorder.last(t).sql)
	assert.Equal(t, []driver.Value{int64(1)}, recorder.last(t).args)

	require.NotNil(t, book.Author, "a present pointer FK must resolve the relation")
	assert.Equal(t, uint64(1), book.Author.ID)
	assert.Equal(t, "Ada", book.Author.Name)
}

// TestEagerWithBelongsTo_PointerForeignKeyResolves drives the eager path: the parent read hydrates
// the *uint64 FK from the database, and the eager load's match step must key the parent by the
// pointed-to value. Before the fix the relation stayed nil after Get even though the row existed.
func TestEagerWithBelongsTo_PointerForeignKeyResolves(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &relPtrBook{})
	q = q.With("Author").(*OrmBuilder)

	recorder.returnRows(`from "rel_ptr_books"`, []string{"id", "title", "author_id"},
		[]driver.Value{int64(7), "first", int64(1)},
	)
	recorder.returnRows(`from "rel_ptr_authors"`, []string{"id", "name"},
		[]driver.Value{int64(1), "Ada"},
	)

	var books []relPtrBook
	require.NoError(t, q.Get(&books))
	require.Len(t, books, 1)

	// The FK hydrated into the pointer field from the parent read.
	require.NotNil(t, books[0].AuthorID, "the parent read must hydrate the pointer FK")
	assert.Equal(t, uint64(1), *books[0].AuthorID)

	require.NotNil(t, books[0].Author, "a present pointer FK must match the eager dictionary")
	assert.Equal(t, uint64(1), books[0].Author.ID)
	assert.Equal(t, "Ada", books[0].Author.Name)
}

// TestEagerWithHasMany_PointerForeignKeyOnChildResolves covers the other dictionary side: the
// pointer FK sits on the child rows, whose FK values key the has-many dictionary. Before the fix
// the child's *uint64 stringified to its address and no parent ever matched.
func TestEagerWithHasMany_PointerForeignKeyOnChildResolves(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &relPtrUser{})
	q = q.With("Books").(*OrmBuilder)

	recorder.returnRows(`from "rel_ptr_users"`, []string{"id"},
		[]driver.Value{int64(1)},
	)
	recorder.returnRows(`from "rel_ptr_books"`, []string{"id", "title", "user_id"},
		[]driver.Value{int64(10), "first", int64(1)},
	)

	var users []relPtrUser
	require.NoError(t, q.Get(&users))
	require.Len(t, users, 1)
	require.Len(t, users[0].Books, 1, "a child pointer FK must key the has-many dictionary by value")
	assert.Equal(t, uint64(10), users[0].Books[0].ID)
	assert.Equal(t, "first", users[0].Books[0].Title)
}

// TestDictKeyPointerValues pins the unwrapping: a pointer value keys the dictionary by the value
// it addresses, and a nil pointer has no key.
func TestDictKeyPointerValues(t *testing.T) {
	ptr := uint64(123)
	assert.Equal(t, "123", dictKey(&ptr))

	var nilPtr *uint64
	assert.Equal(t, "", dictKey(nilPtr))

	str := "abc"
	assert.Equal(t, "abc", dictKey(&str))
}

// TestExtractKeysDereferencesPointerField pins the extraction half: the keys a loader hands the IN
// clause are the pointed-to scalars, so a pointer FK binds the same way every scalar key does.
func TestExtractKeysDereferencesPointerField(t *testing.T) {
	s, err := parseSchema(&relPtrBook{})
	assert.NoError(t, err)
	fkField := s.FieldsByDBName["author_id"]
	assert.NotNil(t, fkField)

	q := newDialectQuery(context.Background(), nil, contractsdatabase.Config{}, stubWriter(t), nil, nil, nil, nil, &Conditions{}, nil)

	one, two := uint64(1), uint64(2)
	keys := extractKeys(q, []reflect.Value{
		reflect.ValueOf(relPtrBook{AuthorID: &one}),
		reflect.ValueOf(relPtrBook{AuthorID: &two}),
		reflect.ValueOf(relPtrBook{AuthorID: &one}), // duplicate by value, not by address
		reflect.ValueOf(relPtrBook{}),               // nil pointer, skipped as the zero value
	}, fkField)

	assert.Equal(t, []any{uint64(1), uint64(2)}, keys)
}

// --- driver.Valuer key wrappers -------------------------------------------------------------

// relNullStrAuthor / relNullStrBook: the belongs-to pair with the FK wrapped in sql.NullString
// behind a pointer — a driver.Valuer column keeps its key in its Value(), not in its fields.
type relNullStrAuthor struct {
	ID   uint64 `gorm:"column:id;primaryKey;autoIncrement"`
	Name string `gorm:"column:name"`
}

func (relNullStrAuthor) TableName() string { return "rel_nullstr_authors" }

type relNullStrBook struct {
	ID       uint64            `gorm:"column:id;primaryKey;autoIncrement"`
	Title    string            `gorm:"column:title"`
	AuthorID *sql.NullString   `gorm:"column:author_id"`
	Author   *relNullStrAuthor `gorm:"-"`
}

func (relNullStrBook) TableName() string { return "rel_nullstr_books" }

func (relNullStrBook) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Author": contractsorm.BelongsTo{Related: &relNullStrAuthor{}, ForeignKey: "author_id", OwnerKey: "id"},
	}
}

// TestLoadBelongsTo_NullStringPointerForeignKeyResolves is the regression for the driver.Valuer
// wrapper defect: the FK *sql.NullString carries its key in its Value(), and before the fix the
// key coercion stringified the wrapper itself ("{1 true}"), which never equalled the related
// row's key "1" — the query still ran with the binding database/sql derives, but the dictionary
// lookup missed and Load left Author nil.
func TestLoadBelongsTo_NullStringPointerForeignKeyResolves(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &relNullStrBook{})
	recorder.returnRows(`from "rel_nullstr_authors"`, []string{"id", "name"},
		[]driver.Value{int64(1), "Ada"},
	)

	book := &relNullStrBook{ID: 7, Title: "first", AuthorID: &sql.NullString{String: "1", Valid: true}}

	require.NoError(t, q.Load(book, "Author"))

	assert.Equal(t, `select * from "rel_nullstr_authors" where "rel_nullstr_authors"."id" in (?)`, recorder.last(t).sql)
	assert.Equal(t, []driver.Value{"1"}, recorder.last(t).args)

	require.NotNil(t, book.Author, "a valid driver.Valuer FK must resolve by the value it wraps")
	assert.Equal(t, uint64(1), book.Author.ID)
	assert.Equal(t, "Ada", book.Author.Name)
}

// TestEagerWithBelongsTo_NullStringForeignKeyResolves drives the eager path: the parent read
// hydrates the *sql.NullString FK from a database int, and the match step must key the parent by
// the value the wrapper holds. Before the fix the relation stayed nil after Get.
func TestEagerWithBelongsTo_NullStringForeignKeyResolves(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &relNullStrBook{})
	q = q.With("Author").(*OrmBuilder)

	recorder.returnRows(`from "rel_nullstr_books"`, []string{"id", "title", "author_id"},
		[]driver.Value{int64(7), "first", int64(1)},
	)
	recorder.returnRows(`from "rel_nullstr_authors"`, []string{"id", "name"},
		[]driver.Value{int64(1), "Ada"},
	)

	var books []relNullStrBook
	require.NoError(t, q.Get(&books))
	require.Len(t, books, 1)

	require.NotNil(t, books[0].AuthorID, "the parent read must hydrate the wrapper FK")
	assert.Equal(t, sql.NullString{String: "1", Valid: true}, *books[0].AuthorID)

	require.NotNil(t, books[0].Author, "a hydrated driver.Valuer FK must match the eager dictionary")
	assert.Equal(t, uint64(1), books[0].Author.ID)
	assert.Equal(t, "Ada", books[0].Author.Name)
}

// relNullIntBook: the framework's generator-emitted shape for a nullable bigint FK — the
// sql.NullInt64 value type, no pointer at all (database/migration/model_mapper.go maps it so).
type relNullIntBook struct {
	ID       uint64            `gorm:"column:id;primaryKey;autoIncrement"`
	AuthorID sql.NullInt64     `gorm:"column:author_id"`
	Author   *relNullStrAuthor `gorm:"-"`
}

func (relNullIntBook) TableName() string { return "rel_nullint_books" }

func (relNullIntBook) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Author": contractsorm.BelongsTo{Related: &relNullStrAuthor{}, ForeignKey: "author_id", OwnerKey: "id"},
	}
}

// TestLoadBelongsTo_NullInt64ValueForeignKeyResolves covers the value-typed wrapper: sql.NullInt64
// stringifies to "{1 true}" just like the pointer shape's referent did, so the same non-vacuous
// key coercion must unwrap it through Value().
func TestLoadBelongsTo_NullInt64ValueForeignKeyResolves(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &relNullIntBook{})
	recorder.returnRows(`from "rel_nullstr_authors"`, []string{"id", "name"},
		[]driver.Value{int64(1), "Ada"},
	)

	book := &relNullIntBook{ID: 7, AuthorID: sql.NullInt64{Int64: 1, Valid: true}}

	require.NoError(t, q.Load(book, "Author"))

	assert.Equal(t, `select * from "rel_nullstr_authors" where "rel_nullstr_authors"."id" in (?)`, recorder.last(t).sql)
	assert.Equal(t, []driver.Value{int64(1)}, recorder.last(t).args)

	require.NotNil(t, book.Author, "a valid sql.NullInt64 FK must resolve by the value it wraps")
	assert.Equal(t, uint64(1), book.Author.ID)
	assert.Equal(t, "Ada", book.Author.Name)
}

// TestLoadBelongsTo_InvalidNullStringForeignKeyIsAbsent pins the invalid-wrapper rule: a
// NullString{Valid: false} unwraps to no value at all and must count as absent exactly like a nil
// FK — no related query, relation nil.
func TestLoadBelongsTo_InvalidNullStringForeignKeyIsAbsent(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &relNullStrBook{})

	book := &relNullStrBook{ID: 7, Title: "first", AuthorID: &sql.NullString{Valid: false}}

	require.NoError(t, q.Load(book, "Author"))

	assert.Equal(t, 0, recorder.count(), "an invalid wrapper FK must not execute a statement")
	assert.Nil(t, book.Author)
}

// --- pointer-typed morph TYPE column --------------------------------------------------------

// relMorphPtrRelated is the morph target; relMorphPtrParent holds both morph halves as pointers —
// the nullable shape of a *_type / *_id pair — with the relation field an `any` the loader
// discovers per row.
type relMorphPtrRelated struct {
	ID uint64 `gorm:"column:id;primaryKey;autoIncrement"`
}

func (relMorphPtrRelated) TableName() string { return "rel_morph_ptr_relateds" }

type relMorphPtrParent struct {
	ID         uint64  `gorm:"column:id;primaryKey;autoIncrement"`
	ForeignKey *uint64 `gorm:"column:foreign_key"`
	MorphType  *string `gorm:"column:morph_type"`
	Relation   any     `gorm:"-"`
}

func (relMorphPtrParent) TableName() string { return "rel_morph_ptr_parents" }

func (relMorphPtrParent) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Relation": contractsorm.MorphTo{Name: "relation", TypeColumn: "morph_type", IDColumn: "foreign_key"},
	}
}

// TestLoadMorphTo_PointerTypeColumnResolves is the regression for the morph-type half: with
// MorphType *string holding a registered alias, loadMorphTo before the fix stringified the raw
// field (the pointer address) and the morph map rejected it as an unregistered type; the id half
// already dereferenced, so only the type half failed.
func TestLoadMorphTo_PointerTypeColumnResolves(t *testing.T) {
	morphmap.Reset()
	defer morphmap.Reset()
	morphmap.Register(map[string]any{"rel_ptr_morph": &relMorphPtrRelated{}})

	q, recorder := newRecordingRelQuery(t, &relMorphPtrParent{})
	recorder.returnRows(`from "rel_morph_ptr_relateds"`, []string{"id"},
		[]driver.Value{int64(1)},
	)

	alias, fk := "rel_ptr_morph", uint64(1)
	parent := &relMorphPtrParent{ID: 1, ForeignKey: &fk, MorphType: &alias}

	require.NoError(t, q.Load(parent, "Relation"))

	assert.Equal(t, `select * from "rel_morph_ptr_relateds" where "rel_morph_ptr_relateds"."id" in (?)`, recorder.last(t).sql)
	assert.Equal(t, []driver.Value{int64(1)}, recorder.last(t).args)

	require.NotNil(t, parent.Relation, "a pointer-typed morph type must reach the morph map as the value it addresses")
	related, ok := parent.Relation.(*relMorphPtrRelated)
	require.True(t, ok, "the registered target type must be instantiated")
	assert.Equal(t, uint64(1), related.ID)
}

// TestEagerWithMorphTo_PointerTypeColumnResolves drives the same defect through the eager path:
// the parent read hydrates both pointer halves, and the bucket build must read the type as its
// value. Before the fix the eager load failed with the pointer address as the morph type.
func TestEagerWithMorphTo_PointerTypeColumnResolves(t *testing.T) {
	morphmap.Reset()
	defer morphmap.Reset()
	morphmap.Register(map[string]any{"rel_ptr_morph": &relMorphPtrRelated{}})

	q, recorder := newRecordingRelQuery(t, &relMorphPtrParent{})
	q = q.With("Relation").(*OrmBuilder)

	recorder.returnRows(`from "rel_morph_ptr_parents"`, []string{"id", "foreign_key", "morph_type"},
		[]driver.Value{int64(1), int64(1), "rel_ptr_morph"},
	)
	recorder.returnRows(`from "rel_morph_ptr_relateds"`, []string{"id"},
		[]driver.Value{int64(1)},
	)

	var parents []relMorphPtrParent
	require.NoError(t, q.Get(&parents))
	require.Len(t, parents, 1)

	require.NotNil(t, parents[0].MorphType, "the parent read must hydrate the pointer type column")
	assert.Equal(t, "rel_ptr_morph", *parents[0].MorphType)

	require.NotNil(t, parents[0].Relation, "an eager MorphTo must match a pointer-typed type column by value")
	related, ok := parents[0].Relation.(*relMorphPtrRelated)
	require.True(t, ok)
	assert.Equal(t, uint64(1), related.ID)
}
