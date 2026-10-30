package gorm

import (
	"database/sql/driver"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
)

// hasOneThroughCountry declares a HasOneThrough whose relation field is slice-shaped. The shape is
// the point: a pointer field takes the first row whether or not the loader cut the match, while a
// slice keeps every row it is handed, so only this fixture shows which isMany the dispatch chose.
type hasOneThroughCountry struct {
	ID        uint
	Name      string
	FirstPost []*relPost `gorm:"-"`
}

func (hasOneThroughCountry) TableName() string { return "through_scope_countries" }

func (hasOneThroughCountry) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"FirstPost": contractsorm.HasOneThrough{
			Related: &relPost{},
			Through: &relUser{},
		},
	}
}

// throughScopeCountry declares a HasManyThrough to relPost through relUser with an OnQuery scope,
// so the test can pin that the related read carries the relation's own scope rather than dropping it.
type throughScopeCountry struct {
	ID    uint
	Name  string
	Posts []*relPost `gorm:"-"`
}

func (throughScopeCountry) TableName() string { return "through_scope_countries" }

func (throughScopeCountry) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Posts": contractsorm.HasManyThrough{
			Related: &relPost{},
			Through: &relUser{},
			OnQuery: func(q contractsorm.Query) contractsorm.Query {
				return q.Where("rel_posts.published", 1)
			},
		},
	}
}

// TestLoadThrough_ReadsAreCompiled pins both queries loadThrough builds: the through-table read that
// collects the second keys, and the related-rows read. Both are addressed through the package's child
// builders, so the framework compiler renders the statement instead of GORM. The loader is driven with parent keys
// too, so a change to either half of its body fails the test.
//
// The binding slices are asserted exactly, since their order and arity carry the chunk keys.
func TestLoadThrough_ReadsAreCompiled(t *testing.T) {
	t.Run("through read collects the second keys", func(t *testing.T) {
		q := newRelQueryWith(t, &relCountry{})
		desc, err := resolveRelation(&relCountry{}, "Posts")
		assert.NoError(t, err)
		if err != nil {
			return
		}

		assert.Equal(t, relKindHasManyThrough, desc.kind)
		assert.Equal(t, "rel_users", desc.throughTable)
		assert.Equal(t, "rel_country_id", desc.firstKey)
		assert.Equal(t, "rel_user_id", desc.secondKey)
		assert.Equal(t, "id", desc.secondLocalKey)
		assert.Equal(t, "rel_posts", desc.relatedTable)
		assert.Nil(t, desc.onPivotQuery, "a through relation carries no pivot scope")

		chunk := []any{uint(1), uint(2)}

		// The child is named the way the loader names it: the through table verbatim, the IN column
		// unquoted and table-qualified, and the select list kept bare so the returned column names —
		// the map keys the hydration loop indexes — stay the two key names.
		child := q.pivotChild(desc.throughTable, desc.throughTable+"."+desc.firstKey, chunk,
			[]string{desc.firstKey, desc.secondLocalKey})
		compiled, bindings, err := child.compileSelect()
		assert.NoError(t, err)
		assert.Equal(t,
			`select "rel_country_id", "id" from "rel_users" where "rel_users"."rel_country_id" in (?, ?)`,
			compiled)
		assert.Equal(t, chunk, bindings)

		// No model is attached, so the child starts from the through table alone and inherits none of
		// the parent query's state — the same shape the fresh GORM session had.
		assert.Nil(t, child.conditions.model)
		assert.Nil(t, child.conditions.dest)
		assert.False(t, child.conditions.withTrashed)
		assert.Empty(t, child.conditions.withoutGlobalScopes)
		assert.Empty(t, child.conditions.scopes)
		assert.Len(t, child.query.State().Wheres, 1)
		assert.Equal(t, []any{desc.firstKey, desc.secondLocalKey}, child.query.Selects())
	})

	t.Run("related read is compiled and keeps the related key when pruned", func(t *testing.T) {
		q := newRelQueryWith(t, &relCountry{})
		desc, err := resolveRelation(&relCountry{}, "Posts")
		assert.NoError(t, err)
		if err != nil {
			return
		}

		chunk := []any{uint(7)}
		child := q.relatedChild(desc.relatedTable, desc.relatedTable+"."+desc.secondKey, chunk)
		compiled, bindings, err := child.compileSelect()
		assert.NoError(t, err)
		assert.Equal(t, `select * from "rel_posts" where "rel_posts"."rel_user_id" in (?)`, compiled)
		assert.Equal(t, chunk, bindings)

		// The loader passes the related key as a required column, so it survives column pruning and
		// the dictionary can be keyed off the related rows.
		child.query.SetSelects([]any{"title", desc.secondKey})
		pruned, prunedBindings, err := child.compileSelect()
		assert.NoError(t, err)
		assert.Equal(t, `select "title", "rel_user_id" from "rel_posts" where "rel_posts"."rel_user_id" in (?)`, pruned)
		assert.Equal(t, chunk, prunedBindings)
	})

	t.Run("related read receives the relation scope", func(t *testing.T) {
		q := newRelQueryWith(t, &throughScopeCountry{})
		desc, err := resolveRelation(&throughScopeCountry{}, "Posts")
		assert.NoError(t, err)
		if err != nil {
			return
		}
		assert.NotNil(t, desc.onQuery, "the fixture declares OnQuery")

		chunk := []any{uint(1), uint(2)}
		child := q.relatedChild(desc.relatedTable, desc.relatedTable+"."+desc.secondKey, chunk)
		scoped, ok := desc.onQuery(child).(*OrmBuilder)
		assert.True(t, ok)

		compiled, bindings, err := scoped.compileSelect()
		assert.NoError(t, err)
		// Order: the IN clause first, then the OnQuery scope's own predicate.
		assert.Equal(t,
			`select * from "rel_posts" where "rel_posts"."rel_user_id" in (?, ?) and "rel_posts"."published" = ?`,
			compiled)
		assert.Equal(t, []any{uint(1), uint(2), 1}, bindings)

		// The gorm chain this was once compared against is gone: the query is no longer assembled
		// into a GORM statement, so the compiled statement above is the whole assertion.
	})

	// No parent keys: the loader takes its empty path and never reaches a statement at all, so the
	// relation field is cleared rather than queried for an IN list that cannot match.
	t.Run("no parent keys issues no statement", func(t *testing.T) {
		q := newRelQueryWith(t, &relCountry{})
		desc, err := resolveRelation(&relCountry{}, "Posts")
		assert.NoError(t, err)
		if err != nil {
			return
		}

		assert.NoError(t, q.loadThrough(nil, &relCountry{}, desc, eagerLoadEntry{relation: "Posts"}, nil, true))
	})
	// Drive the loader itself with parent keys. The recording driver hands back one through row,
	// which is what makes loadThrough run its second read, so the assertion lands on the statements
	// the loader built and executed: the through-table read that collects the second keys, then the
	// related read. A change to either half of the loader's body fails the test.
	t.Run("loader reads the through table then the related table", func(t *testing.T) {
		q, recorder := newRecordingRelQuery(t, &relCountry{})
		desc, err := resolveRelation(&relCountry{}, "Posts")
		assert.NoError(t, err)
		if err != nil {
			return
		}

		// One through row links parent 1 to second key 7; the loader then reads the related row for 7.
		recorder.returnRows(`from "`+desc.throughTable+`"`,
			[]string{desc.firstKey, desc.secondLocalKey},
			[]driver.Value{int64(1), int64(7)})

		parents := []reflect.Value{
			reflect.ValueOf(&relCountry{ID: 1}).Elem(),
			reflect.ValueOf(&relCountry{ID: 2}).Elem(),
		}

		assert.NoError(t, q.loadThrough(parents, &relCountry{}, desc, eagerLoadEntry{relation: "Posts"}, nil, true))
		assert.Equal(t, 2, recorder.count(), "the loader reads the through table and then the related table")

		through := recorder.at(t, 0)
		assert.Equal(t,
			`select "rel_country_id", "id" from "rel_users" where "rel_users"."rel_country_id" in (?, ?)`,
			through.sql)
		assert.Equal(t, driverBindings([]any{uint(1), uint(2)}), through.args)

		related := recorder.at(t, 1)
		assert.Equal(t, `select * from "rel_posts" where "rel_posts"."rel_user_id" in (?)`, related.sql)
		// The key comes off the through row as the driver-typed int64, not the uint the parents carried.
		assert.Equal(t, []driver.Value{int64(7)}, related.args)
	})

	// The relation's OnQuery scope must reach the related statement the loader actually runs, layered
	// on top of the second-key IN clause the loader builds.
	t.Run("loader's related read carries the relation scope", func(t *testing.T) {
		q, recorder := newRecordingRelQuery(t, &throughScopeCountry{})
		desc, err := resolveRelation(&throughScopeCountry{}, "Posts")
		assert.NoError(t, err)
		if err != nil {
			return
		}
		assert.NotNil(t, desc.onQuery, "the fixture declares OnQuery")

		recorder.returnRows(`from "`+desc.throughTable+`"`,
			[]string{desc.firstKey, desc.secondLocalKey},
			[]driver.Value{int64(1), int64(7)})

		parents := []reflect.Value{
			reflect.ValueOf(&throughScopeCountry{ID: 1}).Elem(),
			reflect.ValueOf(&throughScopeCountry{ID: 2}).Elem(),
		}

		assert.NoError(t, q.loadThrough(parents, &throughScopeCountry{}, desc, eagerLoadEntry{relation: "Posts"}, nil, true))

		related := recorder.at(t, 1)
		assert.Equal(t,
			`select * from "rel_posts" where "rel_posts"."rel_user_id" in (?) and "rel_posts"."published" = ?`,
			related.sql)
		// The second-key IN clause first, then the scope's own binding.
		assert.Equal(t, []driver.Value{int64(7), int64(1)}, related.args)
	})
}

// TestLoadThrough_HasOneBranch drives the isMany=false side of loadThrough — the HasOneThrough
// relation — with canned through and related rows, so the assignment runs rather than just the
// statement: the parent gets the single related row as a pointer-shaped value, and when several
// related rows come back through different through rows only the first is attached.
func TestLoadThrough_HasOneBranch(t *testing.T) {
	t.Run("assigns the single related row", func(t *testing.T) {
		q, recorder := newRecordingRelQuery(t, &relCountry{})
		desc, err := resolveRelation(&relCountry{}, "FirstPost")
		assert.NoError(t, err)
		if err != nil {
			return
		}
		assert.Equal(t, relKindHasOneThrough, desc.kind)

		// One through row links parent 1 to user 7; one related row hangs off user 7.
		recorder.returnRows(`from "`+desc.throughTable+`"`,
			[]string{desc.firstKey, desc.secondLocalKey},
			[]driver.Value{int64(1), int64(7)},
		)
		recorder.returnRows(`from "`+desc.relatedTable+`"`,
			[]string{"id", "title", "user_id", desc.secondKey},
			[]driver.Value{int64(70), "only", int64(9), int64(7)},
		)

		country := &relCountry{ID: 1}
		parents := []reflect.Value{reflect.ValueOf(country).Elem()}

		assert.NoError(t, q.loadThrough(parents, &relCountry{}, desc, eagerLoadEntry{relation: "FirstPost"}, nil, false))

		assert.Equal(t, 2, recorder.count(), "the loader reads the through table and then the related table")
		if assert.NotNil(t, country.FirstPost, "a has-one-through parent gets the related row itself, not a slice") {
			assert.Equal(t, uint(70), country.FirstPost.ID)
			assert.Equal(t, "only", country.FirstPost.Title)
		}
		assert.Empty(t, country.Posts, "the has-many field is not the one a has-one-through relation assigns")
	})

	t.Run("keeps the first of several related rows", func(t *testing.T) {
		q, recorder := newRecordingRelQuery(t, &relCountry{})
		desc, err := resolveRelation(&relCountry{}, "FirstPost")
		assert.NoError(t, err)
		if err != nil {
			return
		}

		// The same parent reaches two users through the through table, each with its own post: a
		// HasOneThrough keeps one, and it is the first in through-row order.
		recorder.returnRows(`from "`+desc.throughTable+`"`,
			[]string{desc.firstKey, desc.secondLocalKey},
			[]driver.Value{int64(1), int64(7)},
			[]driver.Value{int64(1), int64(8)},
		)
		recorder.returnRows(`from "`+desc.relatedTable+`"`,
			[]string{"id", "title", "user_id", desc.secondKey},
			[]driver.Value{int64(70), "first", int64(9), int64(7)},
			[]driver.Value{int64(80), "second", int64(9), int64(8)},
		)

		country := &relCountry{ID: 1}
		parents := []reflect.Value{reflect.ValueOf(country).Elem()}

		assert.NoError(t, q.loadThrough(parents, &relCountry{}, desc, eagerLoadEntry{relation: "FirstPost"}, nil, false))

		if assert.NotNil(t, country.FirstPost) {
			assert.Equal(t, uint(70), country.FirstPost.ID, "a has-one-through keeps the first related row")
		}
	})

	t.Run("unmatched parent is cleared", func(t *testing.T) {
		q, recorder := newRecordingRelQuery(t, &relCountry{})
		desc, err := resolveRelation(&relCountry{}, "FirstPost")
		assert.NoError(t, err)
		if err != nil {
			return
		}

		recorder.returnRows(`from "`+desc.throughTable+`"`,
			[]string{desc.firstKey, desc.secondLocalKey},
			[]driver.Value{int64(1), int64(7)},
		)
		recorder.returnRows(`from "`+desc.relatedTable+`"`,
			[]string{"id", "title", "user_id", desc.secondKey},
			[]driver.Value{int64(70), "first", int64(9), int64(7)},
		)

		matched := &relCountry{ID: 1}
		// A parent the through read did not return: the field is cleared, even if it held a stale row.
		unmatched := &relCountry{ID: 2, FirstPost: &relPost{ID: 999, Title: "stale"}}
		parents := []reflect.Value{reflect.ValueOf(matched).Elem(), reflect.ValueOf(unmatched).Elem()}

		assert.NoError(t, q.loadThrough(parents, &relCountry{}, desc, eagerLoadEntry{relation: "FirstPost"}, nil, false))

		if assert.NotNil(t, matched.FirstPost) {
			assert.Equal(t, "first", matched.FirstPost.Title)
		}
		assert.Nil(t, unmatched.FirstPost, "a has-one-through parent with no through row has its relation cleared")
	})
}

// TestLoadOneRelation_HasOneThroughFixture pins the dispatch, not just loadThrough: a country whose
// FirstPost field is slice-shaped must still reach the isMany=false branch of loadThrough. It drives
// the public With + Get path, because loadOneRelation is reached only through runEagerLoads.
//
// The parent reaches two users through the through table, each with a post. The has-one branch cuts
// the match to one row before it assigns it, so the slice gets exactly one element; the has-many
// branch would have matched and assigned both.
func TestLoadOneRelation_HasOneThroughFixture(t *testing.T) {
	q, recorder := newRecordingRelQuery(t, &hasOneThroughCountry{})
	q = q.With("FirstPost").(*OrmBuilder)

	recorder.returnRows(`from "through_scope_countries"`, []string{"id", "name"},
		[]driver.Value{int64(1), "Ada"},
	)
	recorder.returnRows(`"through_scope_country_id" in (?)`,
		[]string{"through_scope_country_id", "id"},
		[]driver.Value{int64(1), int64(7)},
		[]driver.Value{int64(1), int64(8)},
	)
	recorder.returnRows(`from "rel_posts"`,
		[]string{"id", "title", "user_id", "rel_user_id"},
		[]driver.Value{int64(70), "first", int64(9), int64(7)},
		[]driver.Value{int64(80), "second", int64(9), int64(8)},
	)

	var countries []hasOneThroughCountry
	assert.NoError(t, q.Get(&countries))

	if !assert.Len(t, countries, 1) {
		return
	}
	country := countries[0]
	if assert.Len(t, country.FirstPost, 1, "a has-one-through keeps the first related row, and only it") {
		assert.Equal(t, uint(70), country.FirstPost[0].ID)
		assert.Equal(t, "first", country.FirstPost[0].Title)
	}
}
