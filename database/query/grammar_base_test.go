package query

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// newTestBuilder returns a builder over the base grammar, which quotes with double quotes and
// binds with "?" exactly like Laravel's generic grammar.
func newTestBuilder() *Builder {
	return NewBuilder(NewBaseGrammar(""))
}

func TestCompileSelect(t *testing.T) {
	tests := []struct {
		name  string
		build func(*Builder) *Builder
		want  string
	}{
		{"bare select", func(b *Builder) *Builder { return b.From("users", "").Select("*") }, `select * from "users"`},
		{"basic where", func(b *Builder) *Builder { return b.From("users", "").Where("id", 1) }, `select * from "users" where "id" = ?`},
		{"two wheres", func(b *Builder) *Builder {
			return b.From("users", "").Where("id", 1).Where("name", "x")
		}, `select * from "users" where "id" = ? and "name" = ?`},
		{"or where", func(b *Builder) *Builder {
			return b.From("users", "").Where("id", 1).OrWhere("name", "x")
		}, `select * from "users" where "id" = ? or "name" = ?`},
		{"where operator", func(b *Builder) *Builder { return b.From("users", "").Where("id", ">", 1) }, `select * from "users" where "id" > ?`},
		{"where null", func(b *Builder) *Builder { return b.From("users", "").WhereNull("id", BooleanAnd, false) }, `select * from "users" where "id" is null`},
		{"where not null", func(b *Builder) *Builder { return b.From("users", "").WhereNotNull("id", BooleanAnd) }, `select * from "users" where "id" is not null`},
		{"where in", func(b *Builder) *Builder {
			return b.From("users", "").WhereIn("id", []any{1, 2}, BooleanAnd, false)
		}, `select * from "users" where "id" in (?, ?)`},
		{"where in empty", func(b *Builder) *Builder {
			return b.From("users", "").WhereIn("id", []any{}, BooleanAnd, false)
		}, `select * from "users" where 0 = 1`},
		{"where not in empty", func(b *Builder) *Builder {
			return b.From("users", "").WhereIn("id", []any{}, BooleanAnd, true)
		}, `select * from "users" where 1 = 1`},
		{"where in raw", func(b *Builder) *Builder {
			return b.From("users", "").WhereIntegerInRaw("id", []any{1, 2}, BooleanAnd, false)
		}, `select * from "users" where "id" in (1, 2)`},
		{"where between", func(b *Builder) *Builder {
			return b.From("users", "").WhereBetween("id", []any{1, 2}, BooleanAnd, false)
		}, `select * from "users" where "id" between ? and ?`},
		{"where not between", func(b *Builder) *Builder {
			return b.From("users", "").WhereBetween("id", []any{1, 2}, BooleanAnd, true)
		}, `select * from "users" where "id" not between ? and ?`},
		{"where column", func(b *Builder) *Builder {
			return b.From("users", "").WhereColumn("a", "=", "b", BooleanAnd)
		}, `select * from "users" where "a" = "b"`},
		{"where raw", func(b *Builder) *Builder {
			return b.From("users", "").WhereRaw("id = ?", []any{1}, BooleanAnd)
		}, `select * from "users" where id = ?`},
		{"where like", func(b *Builder) *Builder {
			return b.From("users", "").WhereLike("name", "%x%", false, BooleanAnd, false)
		}, `select * from "users" where "name" like ?`},
		{"where not like", func(b *Builder) *Builder {
			return b.From("users", "").WhereNotLike("name", "%x%", false, BooleanAnd)
		}, `select * from "users" where "name" not like ?`},
		{"where date", func(b *Builder) *Builder {
			return b.From("users", "").WhereDate("created_at", "2024-01-01")
		}, `select * from "users" where date("created_at") = ?`},
		{"where row values", func(b *Builder) *Builder {
			return b.From("users", "").WhereRowValues([]any{"a", "b"}, "=", []any{1, 2}, BooleanAnd)
		}, `select * from "users" where ("a", "b") = (?, ?)`},
		{"where nested", func(b *Builder) *Builder {
			return b.From("users", "").WhereNested(func(q *Builder) *Builder {
				return q.Where("id", 1).OrWhere("name", "x")
			}, BooleanAnd)
		}, `select * from "users" where ("id" = ? or "name" = ?)`},
		{"where exists", func(b *Builder) *Builder {
			return b.From("users", "").WhereExists(func(q *Builder) *Builder {
				return q.From("posts", "").WhereColumn("posts.user_id", "=", "users.id", BooleanAnd)
			}, BooleanAnd, false)
		}, `select * from "users" where exists (select * from "posts" where "posts"."user_id" = "users"."id")`},
		{"join", func(b *Builder) *Builder {
			return b.From("users", "").Join("posts", "posts.user_id", "=", "users.id", "inner", false)
		}, `select * from "users" inner join "posts" on "posts"."user_id" = "users"."id"`},
		{"group and having", func(b *Builder) *Builder {
			return b.From("users", "").GroupBy("role").Having("role", "=", "admin")
		}, `select * from "users" group by "role" having "role" = ?`},
		{"order limit offset", func(b *Builder) *Builder {
			return b.From("users", "").OrderBy("id", "desc").Limit(10).Offset(5)
		}, `select * from "users" order by "id" desc limit 10 offset 5`},
		{"distinct", func(b *Builder) *Builder {
			return b.From("users", "").Distinct().Select("name")
		}, `select distinct "name" from "users"`},
		{"aggregate", func(b *Builder) *Builder {
			b.From("users", "")
			b.aggregate = &Aggregate{Function: "count", Columns: []any{"*"}}
			return b
		}, `select count(*) as aggregate from "users"`},
		{"exists query", func(b *Builder) *Builder { return b.From("users", "") }, `select exists(select * from "users") as "exists"`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			builder := test.build(newTestBuilder())

			var got string
			if test.name == "exists query" {
				got = builder.Grammar().CompileExists(builder)
			} else {
				got = builder.ToSQL()
			}

			assert.NoError(t, builder.Error())
			assert.Equal(t, test.want, got)
		})
	}
}

func TestCompileWrite(t *testing.T) {
	t.Run("insert", func(t *testing.T) {
		b := newTestBuilder().From("users", "")
		assert.Equal(t, `insert into "users" ("name") values (?)`,
			b.Grammar().CompileInsert(b, []map[string]any{{"name": "x"}}))
	})

	t.Run("insert multiple", func(t *testing.T) {
		b := newTestBuilder().From("users", "")
		assert.Equal(t, `insert into "users" ("name") values (?), (?)`,
			b.Grammar().CompileInsert(b, []map[string]any{{"name": "x"}, {"name": "y"}}))
	})

	t.Run("update", func(t *testing.T) {
		b := newTestBuilder().From("users", "").Where("id", 1)
		assert.Equal(t, `update "users" set "name" = ? where "id" = ?`,
			b.Grammar().CompileUpdate(b, UpdateValues{{Column: "name", Value: "x"}}))
	})

	t.Run("delete", func(t *testing.T) {
		b := newTestBuilder().From("users", "").Where("id", 1)
		assert.Equal(t, `delete from "users" where "id" = ?`, b.Grammar().CompileDelete(b))
	})

	t.Run("truncate", func(t *testing.T) {
		b := newTestBuilder().From("users", "")
		statements := b.Grammar().CompileTruncate(b)
		assert.Len(t, statements, 1)
		assert.Equal(t, `truncate table "users"`, statements[0].SQL)
		assert.Empty(t, statements[0].Bindings)
	})
}

func TestJoinRaw(t *testing.T) {
	tests := []struct {
		name       string
		expression string
		typ        string
		want       string
	}{
		{"bare fragment keeps the given type", "posts on posts.user_id = users.id", "inner", `select * from "users" inner join posts on posts.user_id = users.id`},
		{"whole clause keeps its own keyword", "left join addresses ua on users.id = ua.user_id", "inner", `select * from "users" left join addresses ua on users.id = ua.user_id`},
		{"whole clause with a compound keyword", "left outer join posts on posts.user_id = users.id", "inner", `select * from "users" left outer join posts on posts.user_id = users.id`},
		{"bare join keyword defaults to inner", "join posts on posts.user_id = users.id", "", `select * from "users" inner join posts on posts.user_id = users.id`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			b := newTestBuilder().From("users", "").JoinRaw(test.expression, nil, test.typ)

			assert.Equal(t, test.want, b.ToSQL())
		})
	}
}
