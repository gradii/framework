package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
)

// These tests pin the struct write rule on the relation and Or* write paths against a real sqlite
// database. No loaded values are tracked (user decision, 2026-11-06), so a struct write leaves a zero
// field out — the column keeps its stored value or takes the table default — unless WithZero names
// it. Every table below declares a non-zero default, so a written zero and a left-out column read
// back differently.
//
// Eloquent writes the attributes a model carries and only those, an assigned null/0 included (oracle
// illuminate/database v12.55.1, real sqlite, /tmp/r15v/probe_v.php -> probe_v.out): a hasMany save of
// new Note(['body' => null, 'votes' => 0]) inserts ("body", "votes", "owner_id") with [null, 0, 1] (P5)
// while new Note(['body' => 'x']) inserts ("body", "owner_id") (P5b); associate + save of a hand-built
// existing note carrying votes = 0 updates "votes" = 0 beside "owner_id" (P6); incrementOrCreate's
// create default 0 is inserted (P1); and a create on a builder that ran firstOrCreate(['name' =>
// ...], ['active' => false]) inserts only its own attributes (P7b). WithZero is the Go way to say a
// zero was assigned; the Or* entry points add their map keys to it for that one call.

type withZeroOwner struct {
	ID       uint64             `gorm:"column:id;primaryKey;autoIncrement"`
	Name     string             `gorm:"column:name"`
	Notes    []*withZeroNote    `gorm:"-"`
	Comments []*withZeroComment `gorm:"-"`
}

func (withZeroOwner) TableName() string  { return "wz_owners" }
func (withZeroOwner) MorphClass() string { return "wz_owner" }

func (withZeroOwner) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Notes":    contractsorm.HasMany{Related: &withZeroNote{}, ForeignKey: "owner_id"},
		"Comments": contractsorm.MorphMany{Related: &withZeroComment{}, Name: "commentable"},
	}
}

type withZeroNote struct {
	ID      uint64         `gorm:"column:id;primaryKey;autoIncrement"`
	OwnerID uint64         `gorm:"column:owner_id"`
	Body    string         `gorm:"column:body"`
	Votes   int            `gorm:"column:votes"`
	Owner   *withZeroOwner `gorm:"-"`
}

func (withZeroNote) TableName() string { return "wz_notes" }

func (withZeroNote) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Owner": contractsorm.BelongsTo{Related: &withZeroOwner{}, ForeignKey: "owner_id"},
	}
}

type withZeroComment struct {
	ID              uint64 `gorm:"column:id;primaryKey;autoIncrement"`
	CommentableID   uint64 `gorm:"column:commentable_id"`
	CommentableType string `gorm:"column:commentable_type"`
	Body            string `gorm:"column:body"`
	Votes           int    `gorm:"column:votes"`
	Commentable     any    `gorm:"-"`
}

func (withZeroComment) TableName() string { return "wz_comments" }

func (withZeroComment) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Commentable": contractsorm.MorphTo{Name: "commentable"},
	}
}

type withZeroCounter struct {
	ID     uint64 `gorm:"column:id;primaryKey;autoIncrement"`
	Name   string `gorm:"column:name"`
	Count  int    `gorm:"column:count"`
	Active bool   `gorm:"column:active"`
}

func (withZeroCounter) TableName() string { return "wz_counters" }

func withZeroSchema(t *testing.T, query contractsorm.Query) withZeroOwner {
	t.Helper()

	runRelationSchema(t, query,
		`create table wz_owners (id integer primary key autoincrement, name text)`,
		`create table wz_notes (id integer primary key autoincrement, owner_id integer, body text default 'unset', votes integer not null default 9)`,
		`create table wz_comments (id integer primary key autoincrement, commentable_id integer, commentable_type text, body text default 'unset', votes integer not null default 9)`,
		`create table wz_counters (id integer primary key autoincrement, name text unique, count integer not null default 7, active integer not null default 1)`,
	)

	owner := withZeroOwner{Name: "owner"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&owner))

	return owner
}

func withZeroNoteRow(t *testing.T, query contractsorm.Query, id uint64) withZeroNote {
	t.Helper()

	var row withZeroNote
	require.NoError(t, cloneRelationQuery(t, query).Where("id", id).First(&row))

	return row
}

func withZeroCommentRow(t *testing.T, query contractsorm.Query, id uint64) withZeroComment {
	t.Helper()

	var row withZeroComment
	require.NoError(t, cloneRelationQuery(t, query).Where("id", id).First(&row))

	return row
}

func withZeroCounterRow(t *testing.T, query contractsorm.Query, name string) withZeroCounter {
	t.Helper()

	var row withZeroCounter
	require.NoError(t, cloneRelationQuery(t, query).Where("name", name).First(&row))

	return row
}

// HasMany Save of a hand-built child: the zero field is left out (the table default 9 applies), and
// WithZero named on the relation object reaches the child's insert and its keyed update.
func TestWithZero_HasManySaveOfAHandBuiltChild(t *testing.T) {
	query := newRelationQuery(t)
	owner := withZeroSchema(t, query)

	plain := withZeroNote{Body: "plain"}
	require.NoError(t, cloneRelationQuery(t, query).Related(&owner, "Notes").(contractsorm.HasManyRelation).Save(&plain))
	stored := withZeroNoteRow(t, query, plain.ID)
	assert.Equal(t, owner.ID, stored.OwnerID)
	assert.Equal(t, 9, stored.Votes, "a zero field is not written")

	relation := cloneRelationQuery(t, query).Related(&owner, "Notes")
	relation.WithZero("votes")
	named := withZeroNote{Body: "named"}
	require.NoError(t, relation.(contractsorm.HasManyRelation).Save(&named))
	assert.Equal(t, 0, withZeroNoteRow(t, query, named.ID).Votes, "WithZero on the relation reaches the child's insert")

	// A keyed child: the update leaves the zero votes and body alone, unless WithZero names them.
	_, err := cloneRelationQuery(t, query).Model(&withZeroNote{}).Where("id", plain.ID).Update("votes", 5)
	require.NoError(t, err)
	require.NoError(t, cloneRelationQuery(t, query).Related(&owner, "Notes").(contractsorm.HasManyRelation).Save(&withZeroNote{ID: plain.ID}))
	kept := withZeroNoteRow(t, query, plain.ID)
	assert.Equal(t, 5, kept.Votes)
	assert.Equal(t, "plain", kept.Body)

	keyed := cloneRelationQuery(t, query).Related(&owner, "Notes")
	keyed.WithZero("votes")
	require.NoError(t, keyed.(contractsorm.HasManyRelation).Save(&withZeroNote{ID: plain.ID}))
	cleared := withZeroNoteRow(t, query, plain.ID)
	assert.Equal(t, 0, cleared.Votes, "WithZero on the relation reaches the child's update")
	assert.Equal(t, "plain", cleared.Body, "WithZero does not narrow or widen beyond the named column")
}

// MorphMany Save: the morph form of the HasMany case.
func TestWithZero_MorphManySaveOfAHandBuiltChild(t *testing.T) {
	query := newRelationQuery(t)
	owner := withZeroSchema(t, query)

	plain := withZeroComment{Body: "plain"}
	require.NoError(t, cloneRelationQuery(t, query).Related(&owner, "Comments").(contractsorm.MorphManyRelation).Save(&plain))
	stored := withZeroCommentRow(t, query, plain.ID)
	assert.Equal(t, owner.ID, stored.CommentableID)
	assert.Equal(t, "wz_owner", stored.CommentableType)
	assert.Equal(t, 9, stored.Votes, "a zero field is not written")

	relation := cloneRelationQuery(t, query).Related(&owner, "Comments")
	relation.WithZero("votes")
	named := withZeroComment{Body: "named"}
	require.NoError(t, relation.(contractsorm.MorphManyRelation).Save(&named))
	assert.Equal(t, 0, withZeroCommentRow(t, query, named.ID).Votes, "WithZero on the relation reaches the child's insert")
}

// BelongsTo Associate persists the hand-built child through a keyed Save: its zero fields keep their
// stored values, and WithZero named on the relation object reaches that save.
func TestWithZero_BelongsToAssociateOfAHandBuiltChild(t *testing.T) {
	query := newRelationQuery(t)
	owner := withZeroSchema(t, query)

	seeded := withZeroNote{Body: "seeded", Votes: 5}
	require.NoError(t, cloneRelationQuery(t, query).Create(&seeded))

	child := withZeroNote{ID: seeded.ID}
	require.NoError(t, cloneRelationQuery(t, query).Related(&child, "Owner").(contractsorm.BelongsToRelation).Associate(&owner))
	stored := withZeroNoteRow(t, query, seeded.ID)
	assert.Equal(t, owner.ID, stored.OwnerID)
	assert.Equal(t, 5, stored.Votes, "a zero field is not written")
	assert.Equal(t, "seeded", stored.Body)

	named := withZeroNote{ID: seeded.ID}
	relation := cloneRelationQuery(t, query).Related(&named, "Owner")
	relation.WithZero("votes")
	require.NoError(t, relation.(contractsorm.BelongsToRelation).Associate(&owner))
	cleared := withZeroNoteRow(t, query, seeded.ID)
	assert.Equal(t, 0, cleared.Votes, "WithZero on the relation reaches the associate's save")
	assert.Equal(t, "seeded", cleared.Body)
}

// MorphTo Associate: the morph form of the BelongsTo case.
func TestWithZero_MorphToAssociateOfAHandBuiltChild(t *testing.T) {
	query := newRelationQuery(t)
	owner := withZeroSchema(t, query)

	seeded := withZeroComment{Body: "seeded", Votes: 5}
	require.NoError(t, cloneRelationQuery(t, query).Create(&seeded))

	child := withZeroComment{ID: seeded.ID}
	require.NoError(t, cloneRelationQuery(t, query).Related(&child, "Commentable").(contractsorm.BelongsToRelation).Associate(&owner))
	stored := withZeroCommentRow(t, query, seeded.ID)
	assert.Equal(t, owner.ID, stored.CommentableID)
	assert.Equal(t, "wz_owner", stored.CommentableType)
	assert.Equal(t, 5, stored.Votes, "a zero field is not written")

	named := withZeroComment{ID: seeded.ID}
	relation := cloneRelationQuery(t, query).Related(&named, "Commentable")
	relation.WithZero("votes")
	require.NoError(t, relation.(contractsorm.BelongsToRelation).Associate(&owner))
	assert.Equal(t, 0, withZeroCommentRow(t, query, seeded.ID).Votes, "WithZero on the relation reaches the associate's save")
}

// The Or* entry points write a zero the caller passed in a map — the key joins WithZero — while an
// unrelated zero field stays out; and the keys belong to that one call, so a later write on the same
// handle carries only its own WithZero list (oracle P7b).
func TestWithZero_OrEntryPointMapKeysAreScopedToTheCall(t *testing.T) {
	query := newRelationQuery(t)
	withZeroSchema(t, query)

	handle := cloneRelationQuery(t, query)
	handle.WithZero("count")

	var created withZeroCounter
	require.NoError(t, handle.FirstOrCreate(&created, map[string]any{"name": "first"}, map[string]any{"active": false}))
	first := withZeroCounterRow(t, query, "first")
	assert.False(t, first.Active, "a zero passed in a map is written")
	assert.Equal(t, 0, first.Count, "the handle's own WithZero list still applies inside the call")

	require.NoError(t, handle.Create(&withZeroCounter{Name: "later"}))
	later := withZeroCounterRow(t, query, "later")
	assert.True(t, later.Active, "the map key of the earlier call does not leak into a later write")
	assert.Equal(t, 0, later.Count, "the handle's own WithZero list is kept")

	var untouched withZeroCounter
	require.NoError(t, cloneRelationQuery(t, query).FirstOrCreate(&untouched, map[string]any{"name": "plain"}, map[string]any{"active": false}))
	assert.Equal(t, 7, withZeroCounterRow(t, query, "plain").Count, "an unrelated zero field is not written")

	var updated withZeroCounter
	require.NoError(t, cloneRelationQuery(t, query).UpdateOrCreate(&updated, map[string]any{"name": "later"}, map[string]any{"active": false}))
	assert.False(t, withZeroCounterRow(t, query, "later").Active, "UpdateOrCreate writes a zero passed in its values map")
}

// incrementOrCreate inserts its create default even when it is 0 (oracle P1).
func TestWithZero_IncrementOrCreateWritesAZeroCreateDefault(t *testing.T) {
	query := newRelationQuery(t)
	withZeroSchema(t, query)

	var counter withZeroCounter
	require.NoError(t, cloneRelationQuery(t, query).IncrementOrCreate(&counter, map[string]any{"name": "zero"}, "count", 0))
	assert.Equal(t, 0, withZeroCounterRow(t, query, "zero").Count, "the zero create default is written, not left to the table default 7")

	var stepped withZeroCounter
	require.NoError(t, cloneRelationQuery(t, query).IncrementOrCreate(&stepped, map[string]any{"name": "zero"}, "count", 0))
	assert.Equal(t, 1, withZeroCounterRow(t, query, "zero").Count, "a found row is stepped")
}

// The relation object's Or* forms scope their map keys to the call too: a later Save through the same
// relation object leaves the child's zero field out.
func TestWithZero_RelationOrEntryPointDoesNotLeakIntoALaterSave(t *testing.T) {
	query := newRelationQuery(t)
	owner := withZeroSchema(t, query)

	relation := cloneRelationQuery(t, query).Related(&owner, "Notes").(contractsorm.HasManyRelation)

	var found withZeroNote
	require.NoError(t, relation.FirstOrCreate(&found, map[string]any{"body": "first"}, map[string]any{"votes": 0}))
	assert.Equal(t, 0, withZeroNoteRow(t, query, found.ID).Votes, "a zero passed in a map is written")

	later := withZeroNote{Body: "later"}
	require.NoError(t, relation.Save(&later))
	assert.Equal(t, 9, withZeroNoteRow(t, query, later.ID).Votes, "the earlier call's map key does not leak into a later save")
}
