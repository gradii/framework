package query

import (
	"testing"

	"github.com/gradii/framework/database/query/core"
	"github.com/stretchr/testify/assert"
)

// This file ports the "joins" area of Laravel's tests/Database/DatabaseQueryBuilderTest.php: exactly the
// cases the port plan marks PORTABLE with area joins. Each case rebuilds the corpus chain with our
// Builder and asserts the exact SQL string, and where the corpus also asserts bindings, the binding
// list too. Our ToSQL renders "?" placeholders and double-quoted identifiers for every dialect, so the
// expected strings keep the corpus's base-grammar shapes verbatim.
//
// The join callback receives the join's own *Builder (JoinClause embeds it), so JoinClause-only entry
// points are expressed with their Builder equivalents: on(col, op, col) is WhereColumn with BooleanAnd,
// orOn(...) is WhereColumn/WhereNested with BooleanOr, and a closure passed to on/orOn is WhereNested.
//
// Sub-assertions inside ported methods that are PHP-only are not reproduced, and are reported rather than
// approximated:
//   - testJoinSub, testLeftJoinSub and testRightJoinSub end with expectException(InvalidArgumentException)
//     for an array passed where a subquery belongs. The corpus assertion is about the thrown exception,
//     not about SQL, and our JoinSub records that case as an error value on the builder.
//   - testJoinSub additionally passes an EloquentBuilder, which has no query-package counterpart.
//   - testComplexJoin runs its two assertions a second time; one pass covers the same state.

// TestBasicJoins ports Laravel's testBasicJoins.
func TestBasicJoins(t *testing.T) {
	t.Run("two-argument join defaults the operator", func(t *testing.T) {
		b := newTestBuilder().From("users", "").Select("*").
			Join("contacts", "users.id", "contacts.id", nil, "inner", false)

		assert.Equal(t, `select * from "users" inner join "contacts" on "users"."id" = "contacts"."id"`, b.ToSQL())
	})

	t.Run("inner join then left join", func(t *testing.T) {
		b := newTestBuilder().From("users", "").Select("*").
			Join("contacts", "users.id", "=", "contacts.id", "inner", false).
			LeftJoin("photos", "users.id", "=", "photos.id")

		assert.Equal(t, `select * from "users" inner join "contacts" on "users"."id" = "contacts"."id" left join "photos" on "users"."id" = "photos"."id"`, b.ToSQL())
	})

	t.Run("left join where then join where", func(t *testing.T) {
		b := newTestBuilder().From("users", "").Select("*").
			JoinWhere("photos", "users.id", "=", "bar", "left").
			JoinWhere("photos", "users.id", "=", "foo", "inner")

		assert.Equal(t, `select * from "users" left join "photos" on "users"."id" = ? inner join "photos" on "users"."id" = ?`, b.ToSQL())
		assert.Equal(t, []any{"bar", "foo"}, b.GetBindings())
	})
}

// TestCrossJoins ports Laravel's testCrossJoins.
func TestCrossJoins(t *testing.T) {
	runLaravelCases(t, []laravelCase{
		{"bare cross join", func(b *Builder) *Builder {
			return b.From("sizes", "").Select("*").CrossJoin("colors", nil, nil, nil)
		}, `select * from "sizes" cross join "colors"`},
		{"cross type through join", func(b *Builder) *Builder {
			return b.From("tableB", "").Select("*").Join("tableA", "tableA.column1", "=", "tableB.column2", "cross", false)
		}, `select * from "tableB" cross join "tableA" on "tableA"."column1" = "tableB"."column2"`},
		{"cross join with a condition", func(b *Builder) *Builder {
			return b.From("tableB", "").Select("*").CrossJoin("tableA", "tableA.column1", "=", "tableB.column2")
		}, `select * from "tableB" cross join "tableA" on "tableA"."column1" = "tableB"."column2"`},
	})
}

// TestComplexJoin ports Laravel's testComplexJoin.
func TestComplexJoin(t *testing.T) {
	t.Run("on and or on", func(t *testing.T) {
		b := newTestBuilder().From("users", "").Select("*").Join("contacts", func(j *Builder) *Builder {
			return j.WhereColumn("users.id", "=", "contacts.id", core.BooleanAnd).
				WhereColumn("users.name", "=", "contacts.name", core.BooleanOr)
		}, "", "", "inner", false)

		assert.Equal(t, `select * from "users" inner join "contacts" on "users"."id" = "contacts"."id" or "users"."name" = "contacts"."name"`, b.ToSQL())
	})

	t.Run("where and or where", func(t *testing.T) {
		b := newTestBuilder().From("users", "").Select("*").Join("contacts", func(j *Builder) *Builder {
			return j.Where("users.id", "=", "foo").OrWhere("users.name", "=", "bar")
		}, "", "", "inner", false)

		assert.Equal(t, `select * from "users" inner join "contacts" on "users"."id" = ? or "users"."name" = ?`, b.ToSQL())
		assert.Equal(t, []any{"foo", "bar"}, b.GetBindings())
	})
}

// TestJoinWhereNull ports Laravel's testJoinWhereNull.
func TestJoinWhereNull(t *testing.T) {
	runLaravelCases(t, []laravelCase{
		{"and where null", func(b *Builder) *Builder {
			return b.From("users", "").Select("*").Join("contacts", func(j *Builder) *Builder {
				return j.WhereColumn("users.id", "=", "contacts.id", core.BooleanAnd).
					WhereNull("contacts.deleted_at", core.BooleanAnd, false)
			}, "", "", "inner", false)
		}, `select * from "users" inner join "contacts" on "users"."id" = "contacts"."id" and "contacts"."deleted_at" is null`},
		{"or where null", func(b *Builder) *Builder {
			return b.From("users", "").Select("*").Join("contacts", func(j *Builder) *Builder {
				return j.WhereColumn("users.id", "=", "contacts.id", core.BooleanAnd).
					OrWhereNull("contacts.deleted_at")
			}, "", "", "inner", false)
		}, `select * from "users" inner join "contacts" on "users"."id" = "contacts"."id" or "contacts"."deleted_at" is null`},
	})
}

// TestJoinWhereNotNull ports Laravel's testJoinWhereNotNull.
func TestJoinWhereNotNull(t *testing.T) {
	runLaravelCases(t, []laravelCase{
		{"and where not null", func(b *Builder) *Builder {
			return b.From("users", "").Select("*").Join("contacts", func(j *Builder) *Builder {
				return j.WhereColumn("users.id", "=", "contacts.id", core.BooleanAnd).
					WhereNotNull("contacts.deleted_at", core.BooleanAnd)
			}, "", "", "inner", false)
		}, `select * from "users" inner join "contacts" on "users"."id" = "contacts"."id" and "contacts"."deleted_at" is not null`},
		{"or where not null", func(b *Builder) *Builder {
			return b.From("users", "").Select("*").Join("contacts", func(j *Builder) *Builder {
				return j.WhereColumn("users.id", "=", "contacts.id", core.BooleanAnd).
					OrWhereNotNull("contacts.deleted_at")
			}, "", "", "inner", false)
		}, `select * from "users" inner join "contacts" on "users"."id" = "contacts"."id" or "contacts"."deleted_at" is not null`},
	})
}

// TestJoinWhereIn ports Laravel's testJoinWhereIn.
func TestJoinWhereIn(t *testing.T) {
	t.Run("and where in", func(t *testing.T) {
		b := newTestBuilder().From("users", "").Select("*").Join("contacts", func(j *Builder) *Builder {
			return j.WhereColumn("users.id", "=", "contacts.id", core.BooleanAnd).
				WhereIn("contacts.name", []any{48, "baz", nil}, core.BooleanAnd, false)
		}, "", "", "inner", false)

		assert.Equal(t, `select * from "users" inner join "contacts" on "users"."id" = "contacts"."id" and "contacts"."name" in (?, ?, ?)`, b.ToSQL())
		assert.Equal(t, []any{48, "baz", nil}, b.GetBindings())
	})

	t.Run("or where in", func(t *testing.T) {
		b := newTestBuilder().From("users", "").Select("*").Join("contacts", func(j *Builder) *Builder {
			return j.WhereColumn("users.id", "=", "contacts.id", core.BooleanAnd).
				OrWhereIn("contacts.name", []any{48, "baz", nil})
		}, "", "", "inner", false)

		assert.Equal(t, `select * from "users" inner join "contacts" on "users"."id" = "contacts"."id" or "contacts"."name" in (?, ?, ?)`, b.ToSQL())
		assert.Equal(t, []any{48, "baz", nil}, b.GetBindings())
	})
}

// TestJoinWhereInSubquery ports Laravel's testJoinWhereInSubquery.
func TestJoinWhereInSubquery(t *testing.T) {
	t.Run("and where in subquery", func(t *testing.T) {
		sub := newTestBuilder().Select("name").From("contacts", "").Where("name", "baz")
		b := newTestBuilder().From("users", "").Select("*").Join("contacts", func(j *Builder) *Builder {
			return j.WhereColumn("users.id", "=", "contacts.id", core.BooleanAnd).
				WhereIn("contacts.name", sub, core.BooleanAnd, false)
		}, "", "", "inner", false)

		assert.Equal(t, `select * from "users" inner join "contacts" on "users"."id" = "contacts"."id" and "contacts"."name" in (select "name" from "contacts" where "name" = ?)`, b.ToSQL())
		assert.Equal(t, []any{"baz"}, b.GetBindings())
	})

	t.Run("or where in subquery", func(t *testing.T) {
		sub := newTestBuilder().Select("name").From("contacts", "").Where("name", "baz")
		b := newTestBuilder().From("users", "").Select("*").Join("contacts", func(j *Builder) *Builder {
			return j.WhereColumn("users.id", "=", "contacts.id", core.BooleanAnd).
				OrWhereIn("contacts.name", sub)
		}, "", "", "inner", false)

		assert.Equal(t, `select * from "users" inner join "contacts" on "users"."id" = "contacts"."id" or "contacts"."name" in (select "name" from "contacts" where "name" = ?)`, b.ToSQL())
		assert.Equal(t, []any{"baz"}, b.GetBindings())
	})
}

// TestJoinWhereNotIn ports Laravel's testJoinWhereNotIn.
func TestJoinWhereNotIn(t *testing.T) {
	t.Run("and where not in", func(t *testing.T) {
		b := newTestBuilder().From("users", "").Select("*").Join("contacts", func(j *Builder) *Builder {
			return j.WhereColumn("users.id", "=", "contacts.id", core.BooleanAnd).
				WhereNotIn("contacts.name", []any{48, "baz", nil}, core.BooleanAnd)
		}, "", "", "inner", false)

		assert.Equal(t, `select * from "users" inner join "contacts" on "users"."id" = "contacts"."id" and "contacts"."name" not in (?, ?, ?)`, b.ToSQL())
		assert.Equal(t, []any{48, "baz", nil}, b.GetBindings())
	})

	t.Run("or where not in", func(t *testing.T) {
		b := newTestBuilder().From("users", "").Select("*").Join("contacts", func(j *Builder) *Builder {
			return j.WhereColumn("users.id", "=", "contacts.id", core.BooleanAnd).
				OrWhereNotIn("contacts.name", []any{48, "baz", nil})
		}, "", "", "inner", false)

		assert.Equal(t, `select * from "users" inner join "contacts" on "users"."id" = "contacts"."id" or "contacts"."name" not in (?, ?, ?)`, b.ToSQL())
		assert.Equal(t, []any{48, "baz", nil}, b.GetBindings())
	})
}

// TestJoinsWithNestedConditions ports Laravel's testJoinsWithNestedConditions.
func TestJoinsWithNestedConditions(t *testing.T) {
	t.Run("nested where on a left join", func(t *testing.T) {
		b := newTestBuilder().From("users", "").Select("*").LeftJoin("contacts", func(j *Builder) *Builder {
			return j.WhereColumn("users.id", "=", "contacts.id", core.BooleanAnd).
				WhereNested(func(n *Builder) *Builder {
					return n.Where("contacts.country", "=", "US").OrWhere("contacts.is_partner", "=", 1)
				}, core.BooleanAnd)
		}, "", "")

		assert.Equal(t, `select * from "users" left join "contacts" on "users"."id" = "contacts"."id" and ("contacts"."country" = ? or "contacts"."is_partner" = ?)`, b.ToSQL())
		assert.Equal(t, []any{"US", 1}, b.GetBindings())
	})

	t.Run("nested where and or on", func(t *testing.T) {
		b := newTestBuilder().From("users", "").Select("*").LeftJoin("contacts", func(j *Builder) *Builder {
			return j.WhereColumn("users.id", "=", "contacts.id", core.BooleanAnd).
				Where("contacts.is_active", "=", 1).
				// orOn with a closure is whereNested with an "or" boolean.
				WhereNested(func(n *Builder) *Builder {
					return n.WhereNested(func(m *Builder) *Builder {
						return m.Where("contacts.country", "=", "UK").
							WhereColumn("contacts.type", "=", "users.type", core.BooleanOr)
					}, core.BooleanOr).WhereNested(func(m *Builder) *Builder {
						return m.Where("contacts.country", "=", "US").
							OrWhereNull("contacts.is_partner")
					}, core.BooleanAnd)
				}, core.BooleanOr)
		}, "", "")

		assert.Equal(t, `select * from "users" left join "contacts" on "users"."id" = "contacts"."id" and "contacts"."is_active" = ? or (("contacts"."country" = ? or "contacts"."type" = "users"."type") and ("contacts"."country" = ? or "contacts"."is_partner" is null))`, b.ToSQL())
		assert.Equal(t, []any{1, "UK", "US"}, b.GetBindings())
	})
}

// TestJoinsWithSubqueryCondition ports Laravel's testJoinsWithSubqueryCondition.
func TestJoinsWithSubqueryCondition(t *testing.T) {
	t.Run("where in a subquery", func(t *testing.T) {
		b := newTestBuilder().From("users", "").Select("*").LeftJoin("contacts", func(j *Builder) *Builder {
			return j.WhereColumn("users.id", "=", "contacts.id", core.BooleanAnd).
				WhereIn("contact_type_id", func(q *Builder) *Builder {
					return q.Select("id").From("contact_types", "").
						Where("category_id", "1").
						WhereNull("deleted_at", core.BooleanAnd, false)
				}, core.BooleanAnd, false)
		}, "", "")

		assert.Equal(t, `select * from "users" left join "contacts" on "users"."id" = "contacts"."id" and "contact_type_id" in (select "id" from "contact_types" where "category_id" = ? and "deleted_at" is null)`, b.ToSQL())
		assert.Equal(t, []any{"1"}, b.GetBindings())
	})

	t.Run("where exists", func(t *testing.T) {
		b := newTestBuilder().From("users", "").Select("*").LeftJoin("contacts", func(j *Builder) *Builder {
			return j.WhereColumn("users.id", "=", "contacts.id", core.BooleanAnd).
				WhereExists(func(q *Builder) *Builder {
					return q.SelectRaw("1", nil).From("contact_types", "").
						WhereRaw("contact_types.id = contacts.contact_type_id", nil, core.BooleanAnd).
						Where("category_id", "1").
						WhereNull("deleted_at", core.BooleanAnd, false)
				}, core.BooleanAnd, false)
		}, "", "")

		assert.Equal(t, `select * from "users" left join "contacts" on "users"."id" = "contacts"."id" and exists (select 1 from "contact_types" where contact_types.id = contacts.contact_type_id and "category_id" = ? and "deleted_at" is null)`, b.ToSQL())
		assert.Equal(t, []any{"1"}, b.GetBindings())
	})
}

// TestJoinsWithAdvancedSubqueryCondition ports Laravel's testJoinsWithAdvancedSubqueryCondition.
func TestJoinsWithAdvancedSubqueryCondition(t *testing.T) {
	b := newTestBuilder().From("users", "").Select("*").LeftJoin("contacts", func(j *Builder) *Builder {
		return j.WhereColumn("users.id", "=", "contacts.id", core.BooleanAnd).
			WhereExists(func(q *Builder) *Builder {
				return q.SelectRaw("1", nil).From("contact_types", "").
					WhereRaw("contact_types.id = contacts.contact_type_id", nil, core.BooleanAnd).
					Where("category_id", "1").
					WhereNull("deleted_at", core.BooleanAnd, false).
					WhereIn("level_id", func(l *Builder) *Builder {
						return l.Select("id").From("levels", "").Where("is_active", true)
					}, core.BooleanAnd, false)
			}, core.BooleanAnd, false)
	}, "", "")

	assert.Equal(t, `select * from "users" left join "contacts" on "users"."id" = "contacts"."id" and exists (select 1 from "contact_types" where contact_types.id = contacts.contact_type_id and "category_id" = ? and "deleted_at" is null and "level_id" in (select "id" from "levels" where "is_active" = ?))`, b.ToSQL())
	assert.Equal(t, []any{"1", true}, b.GetBindings())
}

// TestJoinsWithNestedJoins ports Laravel's testJoinsWithNestedJoins.
func TestJoinsWithNestedJoins(t *testing.T) {
	runLaravelCases(t, []laravelCase{
		{"join nested inside a left join", func(b *Builder) *Builder {
			return b.From("users", "").Select("users.id", "contacts.id", "contact_types.id").
				LeftJoin("contacts", func(j *Builder) *Builder {
					return j.WhereColumn("users.id", "=", "contacts.id", core.BooleanAnd).
						Join("contact_types", "contacts.contact_type_id", "=", "contact_types.id", "inner", false)
				}, "", "")
		}, `select "users"."id", "contacts"."id", "contact_types"."id" from "users" left join ("contacts" inner join "contact_types" on "contacts"."contact_type_id" = "contact_types"."id") on "users"."id" = "contacts"."id"`},
	})
}

// TestJoinsWithMultipleNestedJoins ports Laravel's testJoinsWithMultipleNestedJoins.
func TestJoinsWithMultipleNestedJoins(t *testing.T) {
	b := newTestBuilder().From("users", "").
		Select("users.id", "contacts.id", "contact_types.id", "countries.id", "planets.id").
		LeftJoin("contacts", func(j *Builder) *Builder {
			return j.WhereColumn("users.id", "=", "contacts.id", core.BooleanAnd).
				Join("contact_types", "contacts.contact_type_id", "=", "contact_types.id", "inner", false).
				LeftJoin("countries", func(q *Builder) *Builder {
					return q.WhereColumn("contacts.country", "=", "countries.country", core.BooleanAnd).
						Join("planets", func(p *Builder) *Builder {
							return p.WhereColumn("countries.planet_id", "=", "planet.id", core.BooleanAnd).
								Where("planet.is_settled", "=", 1).
								Where("planet.population", ">=", 10000)
						}, "", "", "inner", false)
				}, "", "")
		}, "", "")

	assert.Equal(t, `select "users"."id", "contacts"."id", "contact_types"."id", "countries"."id", "planets"."id" from "users" left join ("contacts" inner join "contact_types" on "contacts"."contact_type_id" = "contact_types"."id" left join ("countries" inner join "planets" on "countries"."planet_id" = "planet"."id" and "planet"."is_settled" = ? and "planet"."population" >= ?) on "contacts"."country" = "countries"."country") on "users"."id" = "contacts"."id"`, b.ToSQL())
	assert.Equal(t, []any{1, 10000}, b.GetBindings())
}

// TestJoinsWithNestedJoinWithAdvancedSubqueryCondition ports Laravel's
// testJoinsWithNestedJoinWithAdvancedSubqueryCondition.
func TestJoinsWithNestedJoinWithAdvancedSubqueryCondition(t *testing.T) {
	b := newTestBuilder().From("users", "").Select("users.id", "contacts.id", "contact_types.id").
		LeftJoin("contacts", func(j *Builder) *Builder {
			return j.WhereColumn("users.id", "=", "contacts.id", core.BooleanAnd).
				Join("contact_types", "contacts.contact_type_id", "=", "contact_types.id", "inner", false).
				WhereExists(func(q *Builder) *Builder {
					return q.Select("*").From("countries", "").
						WhereColumn("contacts.country", "=", "countries.country", core.BooleanAnd).
						Join("planets", func(p *Builder) *Builder {
							return p.WhereColumn("countries.planet_id", "=", "planet.id", core.BooleanAnd).
								Where("planet.is_settled", "=", 1)
						}, "", "", "inner", false).
						Where("planet.population", ">=", 10000)
				}, core.BooleanAnd, false)
		}, "", "")

	assert.Equal(t, `select "users"."id", "contacts"."id", "contact_types"."id" from "users" left join ("contacts" inner join "contact_types" on "contacts"."contact_type_id" = "contact_types"."id") on "users"."id" = "contacts"."id" and exists (select * from "countries" inner join "planets" on "countries"."planet_id" = "planet"."id" and "planet"."is_settled" = ? where "contacts"."country" = "countries"."country" and "planet"."population" >= ?)`, b.ToSQL())
	assert.Equal(t, []any{1, 10000}, b.GetBindings())
}

// TestJoinWithNestedOnCondition ports Laravel's testJoinWithNestedOnCondition, which adds a whole builder
// as a nested on condition through addNestedWhereQuery.
func TestJoinWithNestedOnCondition(t *testing.T) {
	nested := newTestBuilder().Where("contacts.id", 1)
	b := newTestBuilder().From("users", "").Select("users.id").Join("contacts", func(j *Builder) *Builder {
		return j.WhereColumn("users.id", "contacts.id", nil, core.BooleanAnd).
			AddNestedWhereQuery(nested, core.BooleanAnd)
	}, "", "", "inner", false)

	assert.Equal(t, `select "users"."id" from "users" inner join "contacts" on "users"."id" = "contacts"."id" and ("contacts"."id" = ?)`, b.ToSQL())
	assert.Equal(t, []any{1}, b.GetBindings())
}

// TestJoinSub ports Laravel's testJoinSub.
func TestJoinSub(t *testing.T) {
	t.Run("raw sql subquery", func(t *testing.T) {
		b := newTestBuilder().From("users", "").
			JoinSub(`select * from "contacts"`, "sub", "users.id", "=", "sub.id", "inner", false)

		assert.Equal(t, `select * from "users" inner join (select * from "contacts") as "sub" on "users"."id" = "sub"."id"`, b.ToSQL())
	})

	t.Run("callback subquery", func(t *testing.T) {
		b := newTestBuilder().From("users", "").JoinSub(func(q *Builder) *Builder {
			return q.From("contacts", "")
		}, "sub", "users.id", "=", "sub.id", "inner", false)

		assert.Equal(t, `select * from "users" inner join (select * from "contacts") as "sub" on "users"."id" = "sub"."id"`, b.ToSQL())
	})

	t.Run("two subs, one with a value condition", func(t *testing.T) {
		sub1 := newTestBuilder().From("contacts", "").Where("name", "foo")
		sub2 := newTestBuilder().From("contacts", "").Where("name", "bar")
		b := newTestBuilder().From("users", "").
			JoinSub(sub1, "sub1", "users.id", "=", 1, "inner", true).
			JoinSub(sub2, "sub2", "users.id", "=", "sub2.user_id", "inner", false)

		expected := `select * from "users" ` +
			`inner join (select * from "contacts" where "name" = ?) as "sub1" on "users"."id" = ? ` +
			`inner join (select * from "contacts" where "name" = ?) as "sub2" on "users"."id" = "sub2"."user_id"`

		assert.Equal(t, expected, b.ToSQL())
		assert.Equal(t, []any{"foo", 1, "bar"}, b.RawBindings("join"))
	})
}

// TestJoinSubWithPrefix ports Laravel's testJoinSubWithPrefix, where the grammar prefix reaches both the
// from table and the subquery alias.
func TestJoinSubWithPrefix(t *testing.T) {
	b := NewBuilder(NewBaseGrammar("prefix_")).From("users", "").
		JoinSub(`select * from "contacts"`, "sub", "users.id", "=", "sub.id", "inner", false)

	assert.Equal(t, `select * from "prefix_users" inner join (select * from "contacts") as "prefix_sub" on "prefix_users"."id" = "prefix_sub"."id"`, b.ToSQL())
}

// TestLeftJoinSub ports Laravel's testLeftJoinSub.
func TestLeftJoinSub(t *testing.T) {
	runLaravelCases(t, []laravelCase{
		{"left join sub", func(b *Builder) *Builder {
			return b.From("users", "").LeftJoinSub(newTestBuilder().From("contacts", ""), "sub", "users.id", "=", "sub.id")
		}, `select * from "users" left join (select * from "contacts") as "sub" on "users"."id" = "sub"."id"`},
	})
}

// TestRightJoinSub ports Laravel's testRightJoinSub.
func TestRightJoinSub(t *testing.T) {
	runLaravelCases(t, []laravelCase{
		{"right join sub", func(b *Builder) *Builder {
			return b.From("users", "").RightJoinSub(newTestBuilder().From("contacts", ""), "sub", "users.id", "=", "sub.id")
		}, `select * from "users" right join (select * from "contacts") as "sub" on "users"."id" = "sub"."id"`},
	})
}

// TestCrossJoinSubs ports Laravel's testCrossJoinSubs.
func TestCrossJoinSubs(t *testing.T) {
	b := newTestBuilder().
		SelectRaw("(sale / overall.sales) * 100 AS percent_of_total", nil).
		From("sales", "").
		CrossJoinSub(newTestBuilder().SelectRaw("SUM(sale) AS sales", nil).From("sales", ""), "overall")

	assert.Equal(t, `select (sale / overall.sales) * 100 AS percent_of_total from "sales" cross join (select SUM(sale) AS sales from "sales") as "overall"`, b.ToSQL())
}

// TestJoinLateralMariaDb ports Laravel's testJoinLateralMariaDb (corpus L3441-3449): MariaDB shares the
// MySQL grammar but its own MariaDbGrammar refuses lateral joins, throwing a RuntimeException. Laravel's
// test builds the MariaDB builder explicitly (getMariaDbBuilder()); our port selects MariaDbGrammar for
// the "mariadb" driver name and records the refusal as errLateralJoinNotSupported rather than panicking,
// the same shape TestJoinLateralSQLite uses for the base grammar's refusal.
func TestJoinLateralMariaDb(t *testing.T) {
	builder := NewBuilder(NewMariaDbGrammar("")).From("users", "").JoinLateral(func(q *Builder) *Builder {
		return q.From("contacts", "").WhereColumn("contracts.user_id", "=", "users.id", core.BooleanAnd)
	}, "sub", "inner")

	// Laravel's toSql() throws; the port records the error on the builder and keeps building.
	_ = builder.ToSQL()

	assert.ErrorIs(t, builder.Error(), errLateralJoinNotSupported)
}

// TestJoinLateralMariaDbGrammarSelection pins the dialect wiring the case above depends on: the
// "mariadb" driver name resolves to the MariaDB grammar, which quotes identifiers the MySQL way.
func TestJoinLateralMariaDbGrammarSelection(t *testing.T) {
	grammar, known := newTestGrammar("mariadb", "")

	assert.True(t, known)
	assert.Equal(t, "`users`", grammar.WrapTable("users"))
	assert.IsType(t, &MariaDbGrammar{}, grammar)
}
