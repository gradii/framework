package query

import (
	"testing"

	"github.com/goravel/framework/database/query/core"
	"github.com/stretchr/testify/assert"
)

// This file ports the joinLateral family of Laravel's tests/Database/DatabaseQueryBuilderTest.php for
// the new Builder::joinLateral entry point.
//
// Laravel's Builder::joinLateral() (Builder.php:622) resolves a subquery with createSub(), wraps it as
// `(query) as wrapTable($as)`, adds the bindings to the join component, and pushes a JoinLateralClause
// whose type is the join type (default "inner"). The grammar's compileJoins() (Grammar.php:189) detects
// that clause subclass and hands the wrapped expression to compileJoinLateral(): MySQL (MySqlGrammar.php:385)
// and Postgres (PostgresGrammar.php:465) emit "{$type} join lateral {$expression} on true", SQL Server
// (SqlServerGrammar.php:475) maps non-left types to "cross apply" and left to "outer apply", and the base
// Grammar (Grammar.php:215) throws a RuntimeException. Our port records that refusal as
// errLateralJoinNotSupported instead of panicking, so it surfaces through Builder.Error().
//
// Corpus: laravel/framework 12.x, tests/Database/DatabaseQueryBuilderTest.php. The SQL strings were
// confirmed against the oracle at .claude/laravel-oracle/oracle.php.
//
// testJoinLateralMariaDb (corpus L3441-3449) is ported in TestJoinLateralMariaDb
// (laravel_port_joins_test.go): Laravel refuses lateral joins on MariaDB through MariaDbGrammar, and the
// port now models that refusal with a MariaDbGrammar whose CompileJoinLateral returns the empty string,
// so the shared CompileJoins records errLateralJoinNotSupported. The "mariadb" driver name selects that
// grammar in NewGrammar.

// TestJoinLateralMySql ports Laravel's testJoinLateral (corpus L3395-3439): the string, callback and
// builder subquery forms, the two-join binding order, and the rejection of a non-subquery value.
func TestJoinLateralMySql(t *testing.T) {
	const sub = "select * from `contacts` where `contracts`.`user_id` = `users`.`id`"
	const want = "select * from `users` inner join lateral (" + sub + ") as `sub` on true"

	callback := func(q *Builder) *Builder {
		return q.From("contacts", "").WhereColumn("contracts.user_id", "=", "users.id", core.BooleanAnd)
	}

	t.Run("string subquery", func(t *testing.T) {
		builder := NewBuilder(NewMySqlGrammar("")).From("users", "").JoinLateral(sub, "sub", "inner")

		assert.Equal(t, want, builder.ToSQL())
	})

	t.Run("callback subquery", func(t *testing.T) {
		builder := NewBuilder(NewMySqlGrammar("")).From("users", "").JoinLateral(callback, "sub", "inner")

		assert.Equal(t, want, builder.ToSQL())
	})

	t.Run("builder subquery", func(t *testing.T) {
		subQuery := NewBuilder(NewMySqlGrammar("")).From("contacts", "").
			WhereColumn("contracts.user_id", "=", "users.id", core.BooleanAnd)

		builder := NewBuilder(NewMySqlGrammar("")).From("users", "").JoinLateral(subQuery, "sub", "inner")

		assert.Equal(t, want, builder.ToSQL())
	})

	t.Run("two joins keep the binding order", func(t *testing.T) {
		sub1 := NewBuilder(NewMySqlGrammar("")).From("contacts", "").
			WhereColumn("contracts.user_id", "=", "users.id", core.BooleanAnd).Where("name", "foo")
		sub2 := NewBuilder(NewMySqlGrammar("")).From("contacts", "").
			WhereColumn("contracts.user_id", "=", "users.id", core.BooleanAnd).Where("name", "bar")

		builder := NewBuilder(NewMySqlGrammar("")).From("users", "").
			JoinLateral(sub1, "sub1", "inner").JoinLateral(sub2, "sub2", "inner")

		expected := "select * from `users` " +
			"inner join lateral (select * from `contacts` where `contracts`.`user_id` = `users`.`id` and `name` = ?) as `sub1` on true " +
			"inner join lateral (select * from `contacts` where `contracts`.`user_id` = `users`.`id` and `name` = ?) as `sub2` on true"

		assert.Equal(t, expected, builder.ToSQL())
		assert.Equal(t, []any{"foo", "bar"}, builder.RawBindings("join"))
	})

	t.Run("non-subquery value is rejected", func(t *testing.T) {
		// Laravel throws InvalidArgumentException; the port records the error and keeps building.
		builder := NewBuilder(NewMySqlGrammar("")).From("users", "").JoinLateral([]any{"foo"}, "sub", "inner")

		assert.ErrorIs(t, builder.Error(), errSubqueryTypeInvalid)
	})
}

// TestJoinLateralSQLite ports Laravel's testJoinLateralSQLite (corpus L3451-3459): SQLite has no lateral
// joins, so the base grammar refuses. Laravel throws RuntimeException; the port records the error.
func TestJoinLateralSQLite(t *testing.T) {
	builder := NewBuilder(NewSqliteGrammar("")).From("users", "").JoinLateral(func(q *Builder) *Builder {
		return q.From("contacts", "").WhereColumn("contracts.user_id", "=", "users.id", core.BooleanAnd)
	}, "sub", "inner")

	_ = builder.ToSQL()

	assert.ErrorIs(t, builder.Error(), errLateralJoinNotSupported)
}

// TestJoinLateralPostgres ports Laravel's testJoinLateralPostgres (corpus L3461-3469): Postgres spells a
// lateral join the same way MySQL does.
func TestJoinLateralPostgres(t *testing.T) {
	builder := NewBuilder(NewPostgresGrammar("")).From("users", "").JoinLateral(func(q *Builder) *Builder {
		return q.From("contacts", "").WhereColumn("contracts.user_id", "=", "users.id", core.BooleanAnd)
	}, "sub", "inner")

	assert.Equal(t, `select * from "users" inner join lateral (select * from "contacts" where "contracts"."user_id" = "users"."id") as "sub" on true`, builder.ToSQL())
}

// TestJoinLateralSqlServer ports Laravel's testJoinLateralSqlServer (corpus L3471-3479): SQL Server
// renders a lateral join as "cross apply".
func TestJoinLateralSqlServer(t *testing.T) {
	builder := NewBuilder(NewSqlServerGrammar("")).From("users", "").JoinLateral(func(q *Builder) *Builder {
		return q.From("contacts", "").WhereColumn("contracts.user_id", "=", "users.id", core.BooleanAnd)
	}, "sub", "inner")

	assert.Equal(t, `select * from [users] cross apply (select * from [contacts] where [contracts].[user_id] = [users].[id]) as [sub]`, builder.ToSQL())
}

// TestJoinLateralWithPrefix ports Laravel's testJoinLateralWithPrefix (corpus L3481-3487): the alias and
// the FROM table take the connection's table prefix, but a raw string subquery is left alone.
func TestJoinLateralWithPrefix(t *testing.T) {
	const sub = "select * from `contacts` where `contracts`.`user_id` = `users`.`id`"

	builder := NewBuilder(NewMySqlGrammar("prefix_")).From("users", "").JoinLateral(sub, "sub", "inner")

	assert.Equal(t, "select * from `prefix_users` inner join lateral ("+sub+") as `prefix_sub` on true", builder.ToSQL())
}
