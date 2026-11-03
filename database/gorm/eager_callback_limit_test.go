package gorm

import (
	"database/sql/driver"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/database/gorm/relations"
)

// These pins drive the loaders with the With-style constraint callback (entry.Callback) and assert
// the statement a callback's relation-level limit compiles. Laravel resolves the relation on a
// fresh, non-existing parent instance and runs the callback on the relation object itself, so
// `limit` branches into the query builder's groupLimit — the row_number() over (partition by <key>)
// rewrite that caps rows per parent (HasOneOrMany::limit, HasOneOrMany.php:555). The oracle
// (illuminate/database v12.55.1, executed against sqlite from
// /tmp/eager-callback-limit/eager_callback_limit.php) measures E1 for the has-many form and E2 for
// the through form; eagerCallbackTarget in eager_loader.go quotes both.

// TestLoadHasOneOrMany_CallbackLimitReachesTheRelationBranch pins the E1 shape: the callback's
// limit reaches the relation object's group-limit branch and compiles the per-parent rewrite, not
// the plain global limit a bare-builder callback used to produce.
func TestLoadHasOneOrMany_CallbackLimitReachesTheRelationBranch(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &relUser{})
	desc, err := relations.ResolveRelation(&relUser{}, "Books")
	require.NoError(t, err)

	recorder.returnRows(`partition by "rel_books"."user_id"`,
		[]string{"id", "user_id", "title"},
		[]driver.Value{int64(10), int64(1), "first"},
		[]driver.Value{int64(11), int64(1), "second"},
	)

	parents := []reflect.Value{
		reflect.ValueOf(&relUser{ID: 1}).Elem(),
		reflect.ValueOf(&relUser{ID: 2}).Elem(),
	}
	entry := eagerLoadEntry{Relation: "Books", Callback: func(query contractsorm.Query) contractsorm.Query {
		return query.Limit(2)
	}}

	require.NoError(t, q.loadHasOneOrMany(parents, &relUser{}, desc, entry, nil, true))

	stmt := recorder.at(t, 0)
	assert.Equal(t,
		`select * from (select *, row_number() over (partition by "rel_books"."user_id") as "laravel_row" from "rel_books" where "rel_books"."user_id" in (?, ?)) as "laravel_table" where "laravel_row" <= 2 order by "laravel_row"`,
		stmt.sql)
	assert.Equal(t, driverBindings([]any{uint(1), uint(2)}), stmt.args)

	// The rewrite executes: the canned rows land on the parent whose key they carry.
	user := parents[0].Addr().Interface().(*relUser)
	require.Len(t, user.Books, 2)
	assert.Equal(t, "first", user.Books[0].Title)
}

// TestLoadThrough_CallbackLimitCompilesTheGroupLimit pins the E2 shape: the through relation's
// callback limit partitions per far parent by the through table's first key, over the joined eager
// read — the query addEagerConstraints filters.
func TestLoadThrough_CallbackLimitCompilesTheGroupLimit(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &relCountry{})
	desc, err := relations.ResolveRelation(&relCountry{}, "Posts")
	require.NoError(t, err)

	recorder.returnRows(`from "`+desc.ThroughTable+`"`,
		[]string{desc.FirstKey, desc.SecondLocalKey},
		[]driver.Value{int64(1), int64(7)},
	)
	recorder.returnRows(`partition by "rel_users"."rel_country_id"`,
		[]string{"id", "title", "rel_user_id"},
		[]driver.Value{int64(70), "only", int64(7)},
	)

	parents := []reflect.Value{
		reflect.ValueOf(&relCountry{ID: 1}).Elem(),
		reflect.ValueOf(&relCountry{ID: 2}).Elem(),
	}
	entry := eagerLoadEntry{Relation: "Posts", Callback: func(query contractsorm.Query) contractsorm.Query {
		return query.Limit(1)
	}}

	require.NoError(t, q.loadThrough(parents, &relCountry{}, desc, entry, nil, true))

	require.Equal(t, 2, recorder.count(), "the through read, then the joined related read")
	stmt := recorder.at(t, 1)
	assert.Equal(t,
		`select * from (select "rel_posts".*, "rel_users"."rel_country_id" as "laravel_through_key", row_number() over (partition by "rel_users"."rel_country_id") as "laravel_row" from "rel_posts" inner join "rel_users" on "rel_users"."id" = "rel_posts"."rel_user_id" where "rel_users"."rel_country_id" in (?, ?)) as "laravel_table" where "laravel_row" <= 1 order by "laravel_row"`,
		stmt.sql)
	assert.Equal(t, driverBindings([]any{uint(1), uint(2)}), stmt.args)

	country := parents[0].Addr().Interface().(*relCountry)
	require.Len(t, country.Posts, 1, "the partition caps the parent at one row")
}
