package gorm

import (
	"database/sql/driver"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
)

// Eloquent's Builder::with ends in `$this->eagerLoad = array_merge($this->eagerLoad, $eagerLoad)`
// (Builder.php, illuminate/database v12.55.1): a later entry writes over an earlier entry with the
// same relation KEY unconditionally — a plain entry replaces a constrained one exactly as a
// constrained entry replaces a plain one — and the relation keeps the position it first got. The
// dotted walk's fill-missing rule (addNestedWiths) applies within one parse, not across merges, so a
// later dotted name rewrites its parent key as well.
//
// Every shape below is pinned against fresh oracle runs on v12.55.1; the loaded-row sets there are
// {A,B} for an unconstrained posts read and {A} for the constrained one.

// constrainedBooks is the relation callback the oracle's with('posts', fn ($q) => $q->where(...))
// shapes use.
func constrainedBooks(title string) contractsorm.RelationCallback {
	return func(q contractsorm.Query) contractsorm.Query { return q.Where("title", title) }
}

// Plain after constrained: the later plain entry replaces in place, matching array_merge's key
// reuse. Oracle: with('posts', cb)->with('posts') loads {A,B}.
func TestWithMergePrecedence_PlainAfterConstrained(t *testing.T) {
	q := newRelQueryWith(t, &relUser{}).With("Books", constrainedBooks("Go")).With("Books").(*OrmBuilder)

	require.Len(t, q.conditions.eagerLoad, 1, "array_merge keeps one entry per relation key")
	assert.Equal(t, "Books", q.conditions.eagerLoad[0].Relation)
	assert.Nil(t, q.conditions.eagerLoad[0].Callback)
	assert.Nil(t, q.conditions.eagerLoad[0].Columns)
}

// Constrained after plain: the later constrained entry replaces. Oracle:
// with('posts')->with('posts', cb) loads {A}.
func TestWithMergePrecedence_ConstrainedAfterPlain(t *testing.T) {
	q := newRelQueryWith(t, &relUser{}).With("Books").With("Books", constrainedBooks("Go")).(*OrmBuilder)

	require.Len(t, q.conditions.eagerLoad, 1)
	assert.NotNil(t, q.conditions.eagerLoad[0].Callback)
}

// Two constrained entries: the last write wins. Oracle: only the second read ({B}) ran.
func TestWithMergePrecedence_TwoConstrainedLastWins(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &relUser{})
	q = q.With("Books", constrainedBooks("Go")).(*OrmBuilder)
	q = q.With("Books", constrainedBooks("Rust")).(*OrmBuilder)

	recorder.returnRows(`from "rel_users"`, []string{"id", "name"},
		[]driver.Value{int64(1), "Ada"},
	)
	recorder.returnRows(`from "rel_books"`, []string{"title", "user_id"},
		[]driver.Value{"r", int64(1)},
	)

	var users []relUser
	require.NoError(t, q.Get(&users))
	require.Len(t, users, 1)

	require.Equal(t, 2, recorder.count())
	books := recorder.at(t, 1)
	assert.Contains(t, books.sql, `"title" = ?`)
	assert.Contains(t, books.args, "Rust", "the later constrained read is the one that ran")
	assert.NotContains(t, books.args, "Go")
}

// Plain after constrained, end to end: the related read runs without the dropped constraint and
// every row is assigned. Oracle: with('posts', cb)->with('posts') loads {A,B}.
func TestWithMergePrecedence_PlainAfterConstrainedDropsTheConstraint(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &relUser{})
	q = q.With("Books", constrainedBooks("Go")).(*OrmBuilder)
	q = q.With("Books").(*OrmBuilder)

	recorder.returnRows(`from "rel_users"`, []string{"id", "name"},
		[]driver.Value{int64(1), "Ada"},
	)
	recorder.returnRows(`from "rel_books"`, []string{"title", "user_id"},
		[]driver.Value{"a", int64(1)},
		[]driver.Value{"b", int64(1)},
	)

	var users []relUser
	require.NoError(t, q.Get(&users))
	require.Len(t, users, 1)

	require.Equal(t, 2, recorder.count())
	assert.Equal(t, `select * from "rel_books" where "rel_books"."user_id" in (?)`, recorder.at(t, 1).sql)
	require.Len(t, users[0].Books, 2, "the relation loads unconstrained")
}

// A global scope's plain with() is a later write too. Oracle: the scope's with('posts') after
// with('posts', cb) loads {A,B}.
func TestWithMergePrecedence_GlobalScopePlainWithReplacesOuterConstraint(t *testing.T) {
	q := compiledQuery(t, &modelWithEagerLoadGlobalScope{}, Conditions{}).
		With("related", constrainedBooks("Go")).(*OrmBuilder)
	q = q.addGlobalScopes()

	_, _, err := q.compileSelect()
	require.NoError(t, err)

	require.Len(t, q.conditions.eagerLoad, 1)
	assert.Equal(t, "related", q.conditions.eagerLoad[0].Relation)
	assert.Nil(t, q.conditions.eagerLoad[0].Callback, "the scope's plain with('related') is the later write")
}

// A where-closure's with() merges last, like the array_merge Eloquent's where-closure branch does on
// the child's getEagerLoads(). Oracle: with('posts', cb) then a nested with('posts') loads {A,B}.
func TestWithMergePrecedence_NestedWhereClosurePlainWithReplacesOuterConstraint(t *testing.T) {
	q := newRelQueryWith(t, &relUser{}).With("Books", constrainedBooks("Go")).(*OrmBuilder)
	q = q.Where(func(nested contractsorm.Query) contractsorm.Query { return nested.With("Books") }).(*OrmBuilder)

	require.Len(t, q.conditions.eagerLoad, 1)
	assert.Nil(t, q.conditions.eagerLoad[0].Callback)
}

// A later dotted name rewrites its parent key as well. Oracle:
// with('posts', cb)->with('posts.comments') loads {A,B} together with the nested relation.
func TestWithMergePrecedence_DottedNameRewritesItsParentKey(t *testing.T) {
	q := newRelQueryWith(t, &relUser{}).With("Books", constrainedBooks("Go")).With("Books.Author").(*OrmBuilder)

	require.Len(t, q.conditions.eagerLoad, 2)
	assert.Equal(t, "Books", q.conditions.eagerLoad[0].Relation)
	assert.Nil(t, q.conditions.eagerLoad[0].Callback, "the dotted entry replaces the parent's constraint with its default")
	assert.Equal(t, "Books.Author", q.conditions.eagerLoad[1].Relation)
}

// The converse: a later constrained parent keeps the sibling dotted entry. Oracle:
// with('posts.comments')->with('posts', cb) loads {A} together with the nested relation.
func TestWithMergePrecedence_ConstrainedParentKeepsDottedEntry(t *testing.T) {
	q := newRelQueryWith(t, &relUser{}).With("Books.Author").With("Books", constrainedBooks("Go")).(*OrmBuilder)

	require.Len(t, q.conditions.eagerLoad, 2)
	assert.Equal(t, "Books", q.conditions.eagerLoad[0].Relation)
	assert.NotNil(t, q.conditions.eagerLoad[0].Callback)
	assert.Equal(t, "Books.Author", q.conditions.eagerLoad[1].Relation)
}

// Within ONE with() call, Eloquent folds duplicate relation keys with combineConstraints instead of
// replacing them: both constraints survive — the later-listed entry runs first — and the
// earlier-listed entry keeps its select list and its position. Replacement happens only across
// with() calls (array_merge). Oracle (v12.55.1, behavioral): with('posts:id,user_id,name', 'posts')
// still selects the pruned list; with(['posts' => cb, 'posts']) loads {A}; with(['posts' => cb,
// 'posts:id,user_id,name']) loads {B} with the pruned select; with('posts:id,user_id,name',
// 'posts:id,user_id') keeps the first list.

// A later plain entry inside the same call must not drop the earlier select list. Oracle:
// with('posts:id,user_id,name', 'posts') read select "id", "user_id", "name" from "posts".
func TestWithWithinCall_PlainAfterSelectKeepsSelect(t *testing.T) {
	q := newRelQueryWith(t, &relUser{}).With("Books:title", "Books").(*OrmBuilder)

	require.Len(t, q.conditions.eagerLoad, 1)
	assert.Equal(t, "Books", q.conditions.eagerLoad[0].Relation)
	assert.Equal(t, []string{"title"}, q.conditions.eagerLoad[0].Columns)
	assert.Nil(t, q.conditions.eagerLoad[0].Callback)
}

// A later plain entry inside the same call must not drop the earlier constraint. Oracle:
// with(['posts' => cb, 'posts']) loads {A}.
func TestWithWithinCall_PlainAfterConstraintKeepsConstraint(t *testing.T) {
	q := newRelQueryWith(t, &relUser{}).With(map[string]contractsorm.RelationCallback{"Books": constrainedBooks("Go")}, "Books").(*OrmBuilder)

	require.Len(t, q.conditions.eagerLoad, 1)
	assert.NotNil(t, q.conditions.eagerLoad[0].Callback)
	assert.Nil(t, q.conditions.eagerLoad[0].Columns)
}

// A constraint and a select list for the same relation inside one call both survive. Oracle:
// with(['posts' => cb, 'posts:id,user_id,name']) loads {B} with the pruned select.
func TestWithWithinCall_ConstraintAndSelectBothSurvive(t *testing.T) {
	q := newRelQueryWith(t, &relUser{}).With(map[string]contractsorm.RelationCallback{"Books": constrainedBooks("Go")}, "Books:title").(*OrmBuilder)

	require.Len(t, q.conditions.eagerLoad, 1)
	assert.NotNil(t, q.conditions.eagerLoad[0].Callback)
	assert.Equal(t, []string{"title"}, q.conditions.eagerLoad[0].Columns)
}

// Two select lists for the same relation inside one call: the combined closure runs the later
// entry's select() first, so the earlier-listed list survives. Oracle:
// with('posts:id,user_id,name', 'posts:id,user_id') selected "id", "user_id", "name".
func TestWithWithinCall_EarlierSelectListWins(t *testing.T) {
	q := newRelQueryWith(t, &relUser{}).With("Books:title,user_id", "Books:author").(*OrmBuilder)

	require.Len(t, q.conditions.eagerLoad, 1)
	assert.Equal(t, []string{"title", "user_id"}, q.conditions.eagerLoad[0].Columns)
}

// The relation keeps the slot it first got even when a later entry in the same call folds into it.
// Oracle: with(['comments', 'posts' => cb, 'posts']) keeps keys ["comments", "posts"].
func TestWithWithinCall_PositionKeepsFirstSlot(t *testing.T) {
	q := newRelQueryWith(t, &relUser{}).With("Profile", map[string]contractsorm.RelationCallback{"Books": constrainedBooks("Go")}, "Books").(*OrmBuilder)

	require.Len(t, q.conditions.eagerLoad, 2)
	assert.Equal(t, "Profile", q.conditions.eagerLoad[0].Relation)
	assert.Equal(t, "Books", q.conditions.eagerLoad[1].Relation)
}

// End to end: the within-call plain duplicate must not drop the constraint — the related read still
// runs constrained. Oracle: with(['posts' => cb, 'posts']) loads {A}.
func TestWithWithinCall_PlainAfterConstraintStillConstrainsTheRead(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &relUser{})
	q = q.With(map[string]contractsorm.RelationCallback{"Books": constrainedBooks("Go")}, "Books").(*OrmBuilder)

	recorder.returnRows(`from "rel_users"`, []string{"id", "name"},
		[]driver.Value{int64(1), "Ada"},
	)
	recorder.returnRows(`from "rel_books"`, []string{"title", "user_id"},
		[]driver.Value{"Go", int64(1)},
	)

	var users []relUser
	require.NoError(t, q.Get(&users))
	require.Len(t, users, 1)

	require.Equal(t, 2, recorder.count())
	books := recorder.at(t, 1)
	assert.Equal(t, `select * from "rel_books" where "rel_books"."user_id" in (?) and "title" = ?`, books.sql)
	assert.Contains(t, books.args, "Go")
	require.Len(t, users[0].Books, 1)
}
