package query

import (
	"testing"

	"github.com/gradii/framework/database/query/core"
	"github.com/stretchr/testify/assert"
)

// This file ports the dynamic-where cases of Laravel's
// tests/Database/DatabaseQueryBuilderTest.php for the new Builder::dynamicWhere() /
// Builder::addDynamic() pair.
//
// Laravel reaches dynamicWhere() through Builder::__call(): a call such as $builder->whereFooBarAndBazOrQux()
// whose name starts with "where" is routed to dynamicWhere(), which drops the first five characters,
// splits the rest on the And/Or connectors that are followed by an uppercase letter
// (preg_split('/(And|Or)(?=[A-Z])/', $finder, -1, PREG_SPLIT_DELIM_CAPTURE)), and adds one equality
// clause per column. Each segment becomes Str::snake($segment) and is compared for "=" against the
// parameter at its running index, joined by the lowercased connector; the connector defaults to "and"
// and changes only when a segment is itself "And" or "Or". Go has no magic dispatch, so the port calls
// DynamicWhere() directly with the full method name and its parameters.
//
// The corpus asserts the calls into where() with a mock; the port instead builds the same chain on a real
// builder and asserts the SQL it compiles to, which is what those calls produce. testDynamicWhereIsNotGreedy
// is ported alongside its listed sibling because it exercises exactly this entry point: it pins the
// "not greedy" split, where a lowercase run after a connector ("Orientation") stays part of the segment
// rather than re-triggering the connector.
//
// Corpus: laravel/framework 12.x @ 0fc645c6737626a1b1636cd234192b0d78b76569,
// tests/Database/DatabaseQueryBuilderTest.php (testJoinsWithAdvancedConditions L3213-3225, testDynamicWhere
// L5184-5195, testDynamicWhereIsNotGreedy L5197-5208). The SQL strings were confirmed against the oracle at
// .claude/laravel-oracle/oracle.php (illuminate/database v12.55.1).

// TestDynamicWhere ports Laravel's testDynamicWhere (corpus L5184-5195): the method name
// "whereFooBarAndBazOrQux" yields where("foo_bar", "=", "corge", "and"), where("baz", "=", "waldo", "and")
// and where("qux", "=", "fred", "or"). Laravel's mock builder has no from; the port adds one so the chain
// can compile to SQL.
func TestDynamicWhere(t *testing.T) {
	builder := newTestBuilder().From("users", "").
		DynamicWhere("whereFooBarAndBazOrQux", []any{"corge", "waldo", "fred"})

	assert.Equal(t, `select * from "users" where "foo_bar" = ? and "baz" = ? or "qux" = ?`, builder.ToSQL())
	assert.Equal(t, []any{"corge", "waldo", "fred"}, builder.GetBindings())
}

// TestDynamicWhereIsNotGreedy ports Laravel's testDynamicWhereIsNotGreedy (corpus L5197-5208): the
// "Or" that opens "Orientation" is not a connector, because the regexp only splits an And/Or that is
// immediately followed by an uppercase letter, so "ios_version", "android_version" and "orientation"
// each take one parameter in order.
func TestDynamicWhereIsNotGreedy(t *testing.T) {
	builder := newTestBuilder().From("users", "").
		DynamicWhere("whereIosVersionAndAndroidVersionOrOrientation", []any{"6.1", "4.2", "Vertical"})

	assert.Equal(t, `select * from "users" where "ios_version" = ? and "android_version" = ? or "orientation" = ?`, builder.ToSQL())
	assert.Equal(t, []any{"6.1", "4.2", "Vertical"}, builder.GetBindings())
}

// TestJoinsWithAdvancedConditions ports Laravel's testJoinsWithAdvancedConditions (corpus L3213-3225):
// the dynamic where runs inside a nested where on a join clause. The two-argument on('users.id',
// 'contacts.id') is JoinClause::on with no operator, which Laravel rewrites to whereColumn(..., '=');
// the port expresses it as WhereColumn with the second column in the operator slot, as the other join
// cases do. Inside the nested group, $j->whereRole('admin') is the dynamic where this item adds.
func TestJoinsWithAdvancedConditions(t *testing.T) {
	builder := newTestBuilder().From("users", "").Select("*").LeftJoin("contacts", func(j *Builder) *Builder {
		return j.WhereColumn("users.id", "contacts.id", nil, core.BooleanAnd).
			Where(func(nested *Builder) *Builder {
				return nested.DynamicWhere("whereRole", []any{"admin"}).
					OrWhereNull("contacts.disabled").
					OrWhereRaw("year(contacts.created_at) = 2016", nil)
			})
	}, "", "")

	assert.Equal(t, `select * from "users" left join "contacts" on "users"."id" = "contacts"."id" and ("role" = ? or "contacts"."disabled" is null or year(contacts.created_at) = 2016)`, builder.ToSQL())
	assert.Equal(t, []any{"admin"}, builder.GetBindings())
}
