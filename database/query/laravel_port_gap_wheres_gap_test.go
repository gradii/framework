package query

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/goravel/framework/errors"
)

// This file ports the "wheres-gap" group of the Laravel port plan: the 35 wheres-family methods of
// laravel/framework 12.x tests/Database/DatabaseQueryBuilderTest.php (commit
// 0fc645c6737626a1b1636cd234192b0d78b76569, v12.43.1). Each case rebuilds the corpus chain with our
// Builder and pins the corpus SQL verbatim on the grammar the corpus names: the base grammar
// (double quotes, "?" bindings) for cases built on getBuilder(), and NewMySqlGrammar("") /
// NewPostgresGrammar("") / NewSqliteGrammar("") / NewSqlServerGrammar("") for the dialect ones. A
// corpus prefix argument (getBuilder(prefix: ...)) is not used by any method here.
//
// Where the corpus also asserts bindings, the case asserts them too; the shared runners
// laravelCase/runLaravelCases and dialectCase/runDialectCases (laravel_port_test.go) compare SQL
// only, so the two tables below add the binding expectation on top of them.
//
// Two corpus details are worth recording up front:
//   - Laravel 12 spells whereValueNotBetween / orWhereValueNotBetween as whereValueBetween(..., not: true);
//     our one entry point is Builder.WhereValueBetween(value, columns, boolean, not).
//   - whereAll / whereAny / whereNone take either (columns, operator, value) or a list of closures;
//     the closure form passes a nil operator and each closure becomes its own nested where.

// wheresGapCase is a laravelCase plus the bindings the corpus asserts for it.
type wheresGapCase struct {
	laravelCase
	bindings []any
}

func runWheresGapCases(t *testing.T, cases []wheresGapCase) {
	t.Helper()

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			builder := test.build(newTestBuilder())

			assert.Equal(t, test.want, builder.ToSQL())
			// A builder with no bindings returns a nil slice, so an empty expectation is nil.
			assert.Equal(t, test.bindings, builder.GetBindings())
		})
	}
}

// wheresGapDialectCase is a dialectCase plus the bindings the corpus asserts for it.
type wheresGapDialectCase struct {
	dialectCase
	bindings []any
}

func runWheresGapDialectCases(t *testing.T, cases []wheresGapDialectCase) {
	t.Helper()

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			builder := test.build(NewBuilder(test.grammar))

			assert.Equal(t, test.want, builder.ToSQL())
			assert.Equal(t, test.bindings, builder.GetBindings())
		})
	}
}

// TestArrayWhereNulls ports Laravel's testArrayWhereNulls (corpus L1989-2001). whereNull with a list
// expands to one clause per column, and orWhereNull with a list carries the connector onto every one
// of them, so the first clause is stripped of its "or" by removeLeadingBoolean.
func TestArrayWhereNulls(t *testing.T) {
	runWheresGapCases(t, []wheresGapCase{
		{laravelCase{"where null array", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereNull([]any{"id", "expires_at"}, BooleanAnd, false)
		}, `select * from "users" where "id" is null and "expires_at" is null`}, nil},
		{laravelCase{"or where null array", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", "=", 1).OrWhereNull([]any{"id", "expires_at"})
		}, `select * from "users" where "id" = ? or "id" is null or "expires_at" is null`}, []any{1}},
	})
}

// TestArrayWhereNotNulls ports Laravel's testArrayWhereNotNulls (corpus L2015-2027), the negated
// counterpart of testArrayWhereNulls.
func TestArrayWhereNotNulls(t *testing.T) {
	runWheresGapCases(t, []wheresGapCase{
		{laravelCase{"where not null array", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereNotNull([]any{"id", "expires_at"}, BooleanAnd)
		}, `select * from "users" where "id" is not null and "expires_at" is not null`}, nil},
		{laravelCase{"or where not null array", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", ">", 1).OrWhereNotNull([]any{"id", "expires_at"})
		}, `select * from "users" where "id" > ? or "id" is not null or "expires_at" is not null`}, []any{1}},
	})
}

// TestBasicWhereNulls ports Laravel's testBasicWhereNulls (corpus L1935-1947).
func TestBasicWhereNulls(t *testing.T) {
	runWheresGapCases(t, []wheresGapCase{
		{laravelCase{"where null", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereNull("id", BooleanAnd, false)
		}, `select * from "users" where "id" is null`}, nil},
		{laravelCase{"or where null", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", "=", 1).OrWhereNull("id")
		}, `select * from "users" where "id" = ? or "id" is null`}, []any{1}},
	})
}

// TestBasicWhereNotNulls ports Laravel's testBasicWhereNotNulls (corpus L2002-2014).
func TestBasicWhereNotNulls(t *testing.T) {
	runWheresGapCases(t, []wheresGapCase{
		{laravelCase{"where not null", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereNotNull("id", BooleanAnd)
		}, `select * from "users" where "id" is not null`}, nil},
		{laravelCase{"or where not null", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", ">", 1).OrWhereNotNull("id")
		}, `select * from "users" where "id" > ? or "id" is not null`}, []any{1}},
	})
}

// TestBasicWhereNullExpressionsMysql ports Laravel's testBasicWhereNullExpressionsMysql (corpus
// L1948-1960). The column is a raw expression, so it is emitted verbatim instead of being wrapped.
func TestBasicWhereNullExpressionsMysql(t *testing.T) {
	runWheresGapDialectCases(t, []wheresGapDialectCase{
		{dialectCase{"expression where null", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereNull(Raw("id"), BooleanAnd, false)
		}, "select * from `users` where id is null"}, nil},
		{dialectCase{"expression or where null", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", "=", 1).OrWhereNull(Raw("id"))
		}, "select * from `users` where `id` = ? or id is null"}, []any{1}},
	})
}

// TestBasicOrWheres ports Laravel's testBasicOrWheres (corpus L1280-1287).
func TestBasicOrWheres(t *testing.T) {
	runWheresGapCases(t, []wheresGapCase{
		{laravelCase{"basic or where", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", "=", 1).OrWhere("email", "=", "foo")
		}, `select * from "users" where "id" = ? or "email" = ?`}, []any{1, "foo"}},
	})
}

// TestBasicOrWhereNot ports Laravel's testBasicOrWhereNot (corpus L1288-1295). The negation lives in
// the connector, so the first clause keeps it once removeLeadingBoolean strips only the "or ".
func TestBasicOrWhereNot(t *testing.T) {
	runWheresGapCases(t, []wheresGapCase{
		{laravelCase{"or where not twice", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").OrWhereNot("name", "foo").OrWhereNot("name", "<>", "bar")
		}, `select * from "users" where not "name" = ? or not "name" <> ?`}, []any{"foo", "bar"}},
	})
}

// TestBasicWhereColumn ports Laravel's testBasicWhereColumn (corpus L1470-1482). The two-argument
// form passes the second column in the operator slot; invalidOperator then swaps it into place and
// makes the operator "=", which is why the corpus spells no operator for those clauses.
func TestBasicWhereColumn(t *testing.T) {
	runWheresGapCases(t, []wheresGapCase{
		{laravelCase{"where column without operator", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").
				WhereColumn("first_name", "last_name", "", BooleanAnd).
				OrWhereColumn("first_name", "middle_name", "")
		}, `select * from "users" where "first_name" = "last_name" or "first_name" = "middle_name"`}, nil},
		{laravelCase{"where column with operator", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereColumn("updated_at", ">", "created_at", BooleanAnd)
		}, `select * from "users" where "updated_at" > "created_at"`}, nil},
	})
}

// TestBasicWhereNotIns ports Laravel's testBasicWhereNotIns (corpus L1359-1371).
func TestBasicWhereNotIns(t *testing.T) {
	runWheresGapCases(t, []wheresGapCase{
		{laravelCase{"where not in", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereIn("id", []any{1, 2, 3}, BooleanAnd, true)
		}, `select * from "users" where "id" not in (?, ?, ?)`}, []any{1, 2, 3}},
		{laravelCase{"or where not in", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", "=", 1).WhereIn("id", []any{1, 2, 3}, BooleanOr, true)
		}, `select * from "users" where "id" = ? or "id" not in (?, ?, ?)`}, []any{1, 1, 2, 3}},
	})
}

// TestEmptyWhereIns ports Laravel's testEmptyWhereIns (corpus L1384-1396). An empty list cannot be
// parameterised, so the grammar substitutes a false predicate.
func TestEmptyWhereIns(t *testing.T) {
	runWheresGapCases(t, []wheresGapCase{
		{laravelCase{"empty where in", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereIn("id", []any{}, BooleanAnd, false)
		}, `select * from "users" where 0 = 1`}, nil},
		{laravelCase{"empty or where in", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", 1).WhereIn("id", []any{}, BooleanOr, false)
		}, `select * from "users" where "id" = ? or 0 = 1`}, []any{1}},
	})
}

// TestEmptyWhereNotIns ports Laravel's testEmptyWhereNotIns (corpus L1397-1409).
func TestEmptyWhereNotIns(t *testing.T) {
	runWheresGapCases(t, []wheresGapCase{
		{laravelCase{"empty where not in", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereIn("id", []any{}, BooleanAnd, true)
		}, `select * from "users" where 1 = 1`}, nil},
		{laravelCase{"empty or where not in", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", 1).WhereIn("id", []any{}, BooleanOr, true)
		}, `select * from "users" where "id" = ? or 1 = 1`}, []any{1}},
	})
}

// TestEmptyWhereIntegerInRaw ports Laravel's testEmptyWhereIntegerInRaw (corpus L1454-1461).
func TestEmptyWhereIntegerInRaw(t *testing.T) {
	runWheresGapCases(t, []wheresGapCase{
		{laravelCase{"empty integer in raw", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereIntegerInRaw("id", []any{}, BooleanAnd, false)
		}, `select * from "users" where 0 = 1`}, nil},
	})
}

// TestEmptyWhereIntegerNotInRaw ports Laravel's testEmptyWhereIntegerNotInRaw (corpus L1462-1469).
// Corpus: whereIntegerNotInRaw('id', []) is whereIntegerInRaw(..., not: true).
func TestEmptyWhereIntegerNotInRaw(t *testing.T) {
	runWheresGapCases(t, []wheresGapCase{
		{laravelCase{"empty integer not in raw", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereIntegerInRaw("id", []any{}, BooleanAnd, true)
		}, `select * from "users" where 1 = 1`}, nil},
	})
}

// TestRawWhereIns ports Laravel's testRawWhereIns (corpus L1372-1383). A raw expression in the value
// list is inlined by parameterize and contributes no binding, which CleanBindings enforces.
func TestRawWhereIns(t *testing.T) {
	runWheresGapCases(t, []wheresGapCase{
		{laravelCase{"raw where in", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereIn("id", []any{Raw("1")}, BooleanAnd, false)
		}, `select * from "users" where "id" in (1)`}, nil},
		{laravelCase{"raw or where in", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", "=", 1).WhereIn("id", []any{Raw("1")}, BooleanOr, false)
		}, `select * from "users" where "id" = ? or "id" in (1)`}, []any{1}},
	})
}

// TestRawWheres ports Laravel's testRawWheres (corpus L1296-1303).
func TestRawWheres(t *testing.T) {
	runWheresGapCases(t, []wheresGapCase{
		{laravelCase{"raw where", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereRaw("id = ? or email = ?", []any{1, "foo"}, BooleanAnd)
		}, `select * from "users" where id = ? or email = ?`}, []any{1, "foo"}},
	})
}

// TestRawOrWheres ports Laravel's testRawOrWheres (corpus L1304-1311).
func TestRawOrWheres(t *testing.T) {
	runWheresGapCases(t, []wheresGapCase{
		{laravelCase{"raw or where", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", "=", 1).OrWhereRaw("email = ?", []any{"foo"})
		}, `select * from "users" where "id" = ? or email = ?`}, []any{1, "foo"}},
	})
}

// TestNestedWheres ports Laravel's testNestedWheres (corpus L2900-2909). orWhere with a closure
// nests the closure's conditions, and the nested builder's where bindings are appended after the
// parent's.
func TestNestedWheres(t *testing.T) {
	runWheresGapCases(t, []wheresGapCase{
		{laravelCase{"nested or where", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("email", "=", "foo").OrWhere(func(q *Builder) *Builder {
				return q.Where("name", "=", "bar").Where("age", "=", 25)
			})
		}, `select * from "users" where "email" = ? or ("name" = ? and "age" = ?)`}, []any{"foo", "bar", 25}},
	})
}

// TestNestedWhereBindings ports Laravel's testNestedWhereBindings (corpus L2910-2918). The corpus
// asserts bindings only. The nested query's selectRaw('?', ['ignore']) binding is dropped, because
// addNestedWhereQuery merges the child's where bindings and nothing else, so "ignore" never reaches
// the parent.
func TestNestedWhereBindings(t *testing.T) {
	builder := newTestBuilder().Where("email", "=", "foo").Where(func(q *Builder) *Builder {
		q.SelectRaw("?", []any{"ignore"})

		return q.Where("name", "=", "bar")
	})

	assert.Equal(t, []any{"foo", "bar"}, builder.GetBindings())
}

// TestWhereNot ports Laravel's testWhereNot (corpus L2919-2942). whereNot with a closure becomes a
// nested where whose connector carries the "not"; the first clause keeps it after the leading "and "
// is stripped, the later ones keep "and not" / "or not".
func TestWhereNot(t *testing.T) {
	runWheresGapCases(t, []wheresGapCase{
		{laravelCase{"where not closure", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereNot(func(q *Builder) *Builder {
				return q.Where("email", "=", "foo")
			})
		}, `select * from "users" where not ("email" = ?)`}, []any{"foo"}},
		{laravelCase{"where then where not closure", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("name", "=", "bar").WhereNot(func(q *Builder) *Builder {
				return q.Where("email", "=", "foo")
			})
		}, `select * from "users" where "name" = ? and not ("email" = ?)`}, []any{"bar", "foo"}},
		{laravelCase{"or where not closure", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("name", "=", "bar").OrWhereNot(func(q *Builder) *Builder {
				return q.Where("email", "=", "foo")
			})
		}, `select * from "users" where "name" = ? or not ("email" = ?)`}, []any{"bar", "foo"}},
	})
}

// TestWhereBetweenColumns ports Laravel's testWhereBetweenColumns (corpus L1138-1155). Both bounds
// are columns, so they are wrapped rather than parameterised, and a raw bound is emitted verbatim.
func TestWhereBetweenColumns(t *testing.T) {
	runWheresGapCases(t, []wheresGapCase{
		{laravelCase{"between columns", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").
				WhereBetweenColumns("id", []any{"users.created_at", "users.updated_at"}, BooleanAnd, false)
		}, `select * from "users" where "id" between "users"."created_at" and "users"."updated_at"`}, nil},
		{laravelCase{"not between columns", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").
				WhereBetweenColumns("id", []any{"created_at", "updated_at"}, BooleanAnd, true)
		}, `select * from "users" where "id" not between "created_at" and "updated_at"`}, nil},
		{laravelCase{"between columns raw", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").
				WhereBetweenColumns("id", []any{Raw("1"), Raw("2")}, BooleanAnd, false)
		}, `select * from "users" where "id" between 1 and 2`}, nil},
	})
}

// TestOrWhereBetweenColumns ports Laravel's testOrWhereBetweenColumns (corpus L1156-1173).
func TestOrWhereBetweenColumns(t *testing.T) {
	runWheresGapCases(t, []wheresGapCase{
		{laravelCase{"or between qualified columns", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", 2).
				WhereBetweenColumns("id", []any{"users.created_at", "users.updated_at"}, BooleanOr, false)
		}, `select * from "users" where "id" = ? or "id" between "users"."created_at" and "users"."updated_at"`}, []any{2}},
		{laravelCase{"or between columns", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", 2).
				WhereBetweenColumns("id", []any{"created_at", "updated_at"}, BooleanOr, false)
		}, `select * from "users" where "id" = ? or "id" between "created_at" and "updated_at"`}, []any{2}},
		{laravelCase{"or between raw columns", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", 2).
				WhereBetweenColumns("id", []any{Raw("1"), Raw("2")}, BooleanOr, false)
		}, `select * from "users" where "id" = ? or "id" between 1 and 2`}, []any{2}},
	})
}

// TestOrWhereNotBetweenColumns ports Laravel's testOrWhereNotBetweenColumns (corpus L1174-1191).
func TestOrWhereNotBetweenColumns(t *testing.T) {
	runWheresGapCases(t, []wheresGapCase{
		{laravelCase{"or not between qualified columns", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", 2).
				WhereBetweenColumns("id", []any{"users.created_at", "users.updated_at"}, BooleanOr, true)
		}, `select * from "users" where "id" = ? or "id" not between "users"."created_at" and "users"."updated_at"`}, []any{2}},
		{laravelCase{"or not between columns", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", 2).
				WhereBetweenColumns("id", []any{"created_at", "updated_at"}, BooleanOr, true)
		}, `select * from "users" where "id" = ? or "id" not between "created_at" and "updated_at"`}, []any{2}},
		{laravelCase{"or not between raw columns", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", 2).
				WhereBetweenColumns("id", []any{Raw("1"), Raw("2")}, BooleanOr, true)
		}, `select * from "users" where "id" = ? or "id" not between 1 and 2`}, []any{2}},
	})
}

// TestWhereValueBetween ports Laravel's testWhereValueBetween (corpus L1192-1213). The value is
// parameterised and the two bounds are columns.
func TestWhereValueBetween(t *testing.T) {
	runWheresGapCases(t, []wheresGapCase{
		{laravelCase{"value between columns", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").
				WhereValueBetween("2020-01-01 19:30:00", []any{"created_at", "updated_at"}, BooleanAnd, false)
		}, `select * from "users" where ? between "created_at" and "updated_at"`}, []any{"2020-01-01 19:30:00"}},
		{laravelCase{"value between columns again", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").
				WhereValueBetween("2020-01-01 19:30:00", []any{"created_at", "updated_at"}, BooleanAnd, false)
		}, `select * from "users" where ? between "created_at" and "updated_at"`}, []any{"2020-01-01 19:30:00"}},
		{laravelCase{"value between raw columns", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").
				WhereValueBetween("2020-01-01 19:30:00", []any{Raw("1"), Raw("2")}, BooleanAnd, false)
		}, `select * from "users" where ? between 1 and 2`}, []any{"2020-01-01 19:30:00"}},
	})

	// The last block is raw on both sides; the corpus asserts the SQL only, so the case does too.
	builder := newTestBuilder().Select("*").From("users", "").
		WhereValueBetween(Raw("1"), []any{"created_at", "updated_at"}, BooleanAnd, false)

	assert.Equal(t, `select * from "users" where 1 between "created_at" and "updated_at"`, builder.ToSQL())
}

// TestWhereValueNotBetween ports Laravel's testWhereValueNotBetween (corpus L1236-1257), which is
// whereValueBetween(..., not: true) in Laravel 12.
func TestWhereValueNotBetween(t *testing.T) {
	runWheresGapCases(t, []wheresGapCase{
		{laravelCase{"value not between columns", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").
				WhereValueBetween("2020-01-01 19:30:00", []any{"created_at", "updated_at"}, BooleanAnd, true)
		}, `select * from "users" where ? not between "created_at" and "updated_at"`}, []any{"2020-01-01 19:30:00"}},
		{laravelCase{"value not between columns again", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").
				WhereValueBetween("2020-01-01 19:30:00", []any{"created_at", "updated_at"}, BooleanAnd, true)
		}, `select * from "users" where ? not between "created_at" and "updated_at"`}, []any{"2020-01-01 19:30:00"}},
		{laravelCase{"value not between raw columns", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").
				WhereValueBetween("2020-01-01 19:30:00", []any{Raw("1"), Raw("2")}, BooleanAnd, true)
		}, `select * from "users" where ? not between 1 and 2`}, []any{"2020-01-01 19:30:00"}},
	})

	// The raw-value block again asserts SQL only in the corpus.
	builder := newTestBuilder().Select("*").From("users", "").
		WhereValueBetween(Raw("1"), []any{"created_at", "updated_at"}, BooleanAnd, true)

	assert.Equal(t, `select * from "users" where 1 not between "created_at" and "updated_at"`, builder.ToSQL())
}

// TestOrWhereValueBetween ports Laravel's testOrWhereValueBetween (corpus L1214-1235). Laravel 12
// spells orWhereValueNotBetween as orWhereValueBetween(..., not: true).
func TestOrWhereValueBetween(t *testing.T) {
	runWheresGapCases(t, []wheresGapCase{
		{laravelCase{"or value between", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", 2).
				WhereValueBetween("2020-01-01 19:30:00", []any{"created_at", "updated_at"}, BooleanOr, false)
		}, `select * from "users" where "id" = ? or ? between "created_at" and "updated_at"`}, []any{2, "2020-01-01 19:30:00"}},
		{laravelCase{"or value between again", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", 2).
				WhereValueBetween("2020-01-01 19:30:00", []any{"created_at", "updated_at"}, BooleanOr, false)
		}, `select * from "users" where "id" = ? or ? between "created_at" and "updated_at"`}, []any{2, "2020-01-01 19:30:00"}},
		{laravelCase{"or value between raw columns", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", 2).
				WhereValueBetween("2020-01-01 19:30:00", []any{Raw("1"), Raw("2")}, BooleanOr, false)
		}, `select * from "users" where "id" = ? or ? between 1 and 2`}, []any{2, "2020-01-01 19:30:00"}},
	})

	// The raw-value block asserts SQL only in the corpus.
	builder := newTestBuilder().Select("*").From("users", "").Where("id", 2).
		WhereValueBetween(Raw("1"), []any{"created_at", "updated_at"}, BooleanOr, false)

	assert.Equal(t, `select * from "users" where "id" = ? or 1 between "created_at" and "updated_at"`, builder.ToSQL())
}

// TestOrWhereValueNotBetween ports Laravel's testOrWhereValueNotBetween (corpus L1258-1279).
func TestOrWhereValueNotBetween(t *testing.T) {
	runWheresGapCases(t, []wheresGapCase{
		{laravelCase{"or value not between", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", 2).
				WhereValueBetween("2020-01-01 19:30:00", []any{"created_at", "updated_at"}, BooleanOr, true)
		}, `select * from "users" where "id" = ? or ? not between "created_at" and "updated_at"`}, []any{2, "2020-01-01 19:30:00"}},
		{laravelCase{"or value not between again", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", 2).
				WhereValueBetween("2020-01-01 19:30:00", []any{"created_at", "updated_at"}, BooleanOr, true)
		}, `select * from "users" where "id" = ? or ? not between "created_at" and "updated_at"`}, []any{2, "2020-01-01 19:30:00"}},
		{laravelCase{"or value not between raw columns", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", 2).
				WhereValueBetween("2020-01-01 19:30:00", []any{Raw("1"), Raw("2")}, BooleanOr, true)
		}, `select * from "users" where "id" = ? or ? not between 1 and 2`}, []any{2, "2020-01-01 19:30:00"}},
	})

	// The raw-value block asserts SQL only in the corpus.
	builder := newTestBuilder().Select("*").From("users", "").Where("id", 2).
		WhereValueBetween(Raw("1"), []any{"created_at", "updated_at"}, BooleanOr, true)

	assert.Equal(t, `select * from "users" where "id" = ? or 1 not between "created_at" and "updated_at"`, builder.ToSQL())
}

// TestWhereAll ports Laravel's testWhereAll (corpus L1567-1587). whereAll repeats one condition
// across every column joined by "and", inside one nested group; the closure form nests each closure
// as its own group, which is why the corpus expectation has doubled parentheses.
func TestWhereAll(t *testing.T) {
	runWheresGapCases(t, []wheresGapCase{
		{laravelCase{"where all equality", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereAll([]any{"last_name", "email"}, "=", "%Otwell%", BooleanAnd)
		}, `select * from "users" where ("last_name" = ? and "email" = ?)`}, []any{"%Otwell%", "%Otwell%"}},
		{laravelCase{"where all not like", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereAll([]any{"last_name", "email"}, "not like", "%Otwell%", BooleanAnd)
		}, `select * from "users" where ("last_name" not like ? and "email" not like ?)`}, []any{"%Otwell%", "%Otwell%"}},
		{laravelCase{"where all closures", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereAll([]any{
				func(q *Builder) *Builder { return q.Where("last_name", "like", "%Otwell%") },
				func(q *Builder) *Builder { return q.Where("email", "like", "%Otwell%") },
			}, nil, nil, BooleanAnd)
		}, `select * from "users" where (("last_name" like ?) and ("email" like ?))`}, []any{"%Otwell%", "%Otwell%"}},
	})
}

// TestWhereAny ports Laravel's testWhereAny (corpus L1614-1634), the "or" counterpart of whereAll.
func TestWhereAny(t *testing.T) {
	runWheresGapCases(t, []wheresGapCase{
		{laravelCase{"where any like", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereAny([]any{"last_name", "email"}, "like", "%Otwell%", BooleanAnd)
		}, `select * from "users" where ("last_name" like ? or "email" like ?)`}, []any{"%Otwell%", "%Otwell%"}},
		{laravelCase{"where any equality", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereAny([]any{"last_name", "email"}, "=", "%Otwell%", BooleanAnd)
		}, `select * from "users" where ("last_name" = ? or "email" = ?)`}, []any{"%Otwell%", "%Otwell%"}},
		{laravelCase{"where any closures", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereAny([]any{
				func(q *Builder) *Builder { return q.Where("last_name", "like", "%Otwell%") },
				func(q *Builder) *Builder { return q.Where("email", "like", "%Otwell%") },
			}, nil, nil, BooleanAnd)
		}, `select * from "users" where (("last_name" like ?) or ("email" like ?))`}, []any{"%Otwell%", "%Otwell%"}},
	})
}

// TestWhereNone ports Laravel's testWhereNone (corpus L1661-1686). whereNone is whereAny with the
// negation folded into the connector, so the whole group is negated as one.
func TestWhereNone(t *testing.T) {
	runWheresGapCases(t, []wheresGapCase{
		{laravelCase{"where none like", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereNone([]any{"last_name", "email"}, "like", "%Otwell%", BooleanAnd)
		}, `select * from "users" where not ("last_name" like ? or "email" like ?)`}, []any{"%Otwell%", "%Otwell%"}},
		{laravelCase{"where none equality", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereNone([]any{"last_name", "email"}, "=", "Otwell", BooleanAnd)
		}, `select * from "users" where not ("last_name" = ? or "email" = ?)`}, []any{"Otwell", "Otwell"}},
		{laravelCase{"where then where none", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").
				Where("first_name", "like", "%Taylor%").
				WhereNone([]any{"last_name", "email"}, "like", "%Otwell%", BooleanAnd)
		}, `select * from "users" where "first_name" like ? and not ("last_name" like ? or "email" like ?)`},
			[]any{"%Taylor%", "%Otwell%", "%Otwell%"}},
		{laravelCase{"where none closures", func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereNone([]any{
				func(q *Builder) *Builder { return q.Where("last_name", "like", "%Otwell%") },
				func(q *Builder) *Builder { return q.Where("email", "like", "%Otwell%") },
			}, nil, nil, BooleanAnd)
		}, `select * from "users" where not (("last_name" like ?) or ("email" like ?))`}, []any{"%Otwell%", "%Otwell%"}},
	})
}

// TestWhereLikePostgres ports Laravel's testWhereLikePostgres (corpus L788-815). A like comparison
// written as a plain where keeps the caller's operator spelling, and Postgres casts the column to
// text in front of it.
func TestWhereLikePostgres(t *testing.T) {
	runWheresGapDialectCases(t, []wheresGapDialectCase{
		{dialectCase{"like", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", "like", "1")
		}, `select * from "users" where "id"::text like ?`}, []any{"1"}},
		{dialectCase{"uppercase LIKE", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", "LIKE", "1")
		}, `select * from "users" where "id"::text LIKE ?`}, []any{"1"}},
		{dialectCase{"ilike", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", "ilike", "1")
		}, `select * from "users" where "id"::text ilike ?`}, []any{"1"}},
		{dialectCase{"not like", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", "not like", "1")
		}, `select * from "users" where "id"::text not like ?`}, []any{"1"}},
		{dialectCase{"not ilike", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").Where("id", "not ilike", "1")
		}, `select * from "users" where "id"::text not ilike ?`}, []any{"1"}},
	})
}

// TestWhereLikeClausePostgres ports Laravel's testWhereLikeClausePostgres (corpus L816-848).
// whereLike defaults to a case insensitive match, which Postgres spells ilike.
func TestWhereLikeClausePostgres(t *testing.T) {
	runWheresGapDialectCases(t, []wheresGapDialectCase{
		{dialectCase{"where like default", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereLike("id", "1", false, BooleanAnd, false)
		}, `select * from "users" where "id"::text ilike ?`}, []any{"1"}},
		{dialectCase{"where like case sensitive", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereLike("id", "1", true, BooleanAnd, false)
		}, `select * from "users" where "id"::text like ?`}, []any{"1"}},
		{dialectCase{"where not like default", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereNotLike("id", "1", false, BooleanAnd)
		}, `select * from "users" where "id"::text not ilike ?`}, []any{"1"}},
		{dialectCase{"where not like case sensitive", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereNotLike("id", "1", true, BooleanAnd)
		}, `select * from "users" where "id"::text not like ?`}, []any{"1"}},
	})
}

// TestWhereLikeClauseMysql ports Laravel's testWhereLikeClauseMysql (corpus L849-881). A case
// sensitive match adds MySQL's "binary" modifier after the operator.
func TestWhereLikeClauseMysql(t *testing.T) {
	runWheresGapDialectCases(t, []wheresGapDialectCase{
		{dialectCase{"where like default", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereLike("id", "1", false, BooleanAnd, false)
		}, "select * from `users` where `id` like ?"}, []any{"1"}},
		{dialectCase{"where like explicit insensitive", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereLike("id", "1", false, BooleanAnd, false)
		}, "select * from `users` where `id` like ?"}, []any{"1"}},
		{dialectCase{"where like case sensitive", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereLike("id", "1", true, BooleanAnd, false)
		}, "select * from `users` where `id` like binary ?"}, []any{"1"}},
		{dialectCase{"where not like default", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereNotLike("id", "1", false, BooleanAnd)
		}, "select * from `users` where `id` not like ?"}, []any{"1"}},
		{dialectCase{"where not like insensitive", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereNotLike("id", "1", false, BooleanAnd)
		}, "select * from `users` where `id` not like ?"}, []any{"1"}},
		{dialectCase{"where not like case sensitive", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereNotLike("id", "1", true, BooleanAnd)
		}, "select * from `users` where `id` not like binary ?"}, []any{"1"}},
	})
}

// TestWhereLikeClauseSqlite ports Laravel's testWhereLikeClauseSqlite (corpus L882-919). A case
// sensitive match becomes glob, and the value's glob wildcards are rewritten: % and _ become * and ?,
// while glob's own * and ? are escaped. A non-glob match keeps like and its value untouched.
func TestWhereLikeClauseSqlite(t *testing.T) {
	runWheresGapDialectCases(t, []wheresGapDialectCase{
		{dialectCase{"where like", NewSqliteGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereLike("id", "1", false, BooleanAnd, false)
		}, `select * from "users" where "id" like ?`}, []any{"1"}},
		{dialectCase{"where like case sensitive", NewSqliteGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereLike("id", "1", true, BooleanAnd, false)
		}, `select * from "users" where "id" glob ?`}, []any{"1"}},
		{dialectCase{"where like glob escaping", NewSqliteGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereLike("description", "Hell* _orld?%", true, BooleanAnd, false)
		}, `select * from "users" where "description" glob ?`}, []any{"Hell[*] ?orld[?]*"}},
		{dialectCase{"where not like", NewSqliteGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereNotLike("id", "1", false, BooleanAnd)
		}, `select * from "users" where "id" not like ?`}, []any{"1"}},
		{dialectCase{"where not like glob escaping", NewSqliteGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereNotLike("description", "Hell* _orld?%", true, BooleanAnd)
		}, `select * from "users" where "description" not glob ?`}, []any{"Hell[*] ?orld[?]*"}},
		{dialectCase{"where like and not like glob", NewSqliteGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").
				WhereLike("name", "John%", true, BooleanAnd, false).
				WhereNotLike("name", "%Doe%", true, BooleanAnd)
		}, `select * from "users" where "name" glob ? and "name" not glob ?`}, []any{"John*", "*Doe*"}},
		{dialectCase{"where like then or like", NewSqliteGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").
				WhereLike("name", "John%", false, BooleanAnd, false).
				WhereLike("name", "Jane%", true, BooleanOr, false)
		}, `select * from "users" where "name" like ? or "name" glob ?`}, []any{"John%", "Jane*"}},
	})
}

// TestWhereLikeClauseSqlServer ports Laravel's testWhereLikeClauseSqlServer (corpus L920-937). SQL
// Server has no case sensitive like spelling, and the corpus only exercises the insensitive one;
// orWhereLike is the same whereLike with an "or" connector.
func TestWhereLikeClauseSqlServer(t *testing.T) {
	runWheresGapDialectCases(t, []wheresGapDialectCase{
		{dialectCase{"where like", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereLike("id", "1", false, BooleanAnd, false)
		}, "select * from [users] where [id] like ?"}, []any{"1"}},
		{dialectCase{"where like or where like", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").
				WhereLike("id", "1", false, BooleanAnd, false).
				WhereLike("id", "2", false, BooleanOr, false)
		}, "select * from [users] where [id] like ? or [id] like ?"}, []any{"1", "2"}},
		{dialectCase{"where not like", NewSqlServerGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereNotLike("id", "1", false, BooleanAnd)
		}, "select * from [users] where [id] not like ?"}, []any{"1"}},
	})
}

// TestWhereRowValuesArityMismatch ports Laravel's testWhereRowValuesArityMismatch (corpus
// L6506-6514). Laravel throws InvalidArgumentException because the tuple has one column and two
// values; our builder records errors.QueryRowValuesCountMismatch and surfaces it through Error()
// instead of throwing, so the case asserts the recorded error and that no clause reached the SQL.
//
// Corpus text: expectExceptionMessage('The number of columns must match the number of values').
// errors.QueryRowValuesCountMismatch now carries that text verbatim (errors/list.go); the remaining
// divergence is that we record an error value rather than throwing an exception, which is the
// package's deliberate recording model. The message itself is pinned again in
// laravel_port_fix_align_row_values_message_test.go.
func TestWhereRowValuesArityMismatch(t *testing.T) {
	builder := newTestBuilder().Select("*").From("orders", "").
		WhereRowValues([]any{"last_update"}, "<", []any{1, 2}, BooleanAnd)

	assert.Error(t, builder.Error())
	assert.Equal(t, errors.QueryRowValuesCountMismatch, builder.Error())
	assert.Equal(t, "The number of columns must match the number of values", builder.Error().Error())

	// whereRowValues records nothing when the arity is wrong, so the where clause is absent.
	assert.Equal(t, `select * from "orders"`, builder.ToSQL())
}
