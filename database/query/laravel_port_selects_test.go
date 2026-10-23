package query

import "testing"

// This file ports the Laravel tests/Database/QueryBuilderTest.php cases whose plan area is "selects"
// (see .claude/laravel-oracle/port_plan.md). Each case rebuilds the corpus chain with our Builder and
// asserts the exact SQL the corpus pins. Our ToSQL renders "?" placeholders and double-quoted
// identifiers for every dialect, Postgres included, so no "$N" translation is applied.

// TestBasicTableWrappingProtectsQuotationMarks ports Laravel's testBasicTableWrappingProtectsQuotationMarks.
// corpus L99: select('*')->from('some"table') yields a table name whose embedded quote is doubled.
func TestBasicTableWrappingProtectsQuotationMarks(t *testing.T) {
	runLaravelCases(t, []laravelCase{
		{"doubled quote in table name", func(b *Builder) *Builder {
			return b.From(`some"table`, "").Select("*")
		}, `select * from "some""table"`},
	})
}

// TestAliasWrappingAsWholeConstant ports Laravel's testAliasWrappingAsWholeConstant.
// corpus L106: select('x.y as foo.bar')->from('baz'). The alias stays one wrapped constant.
func TestAliasWrappingAsWholeConstant(t *testing.T) {
	runLaravelCases(t, []laravelCase{
		{"alias as whole constant", func(b *Builder) *Builder {
			return b.From("baz", "").Select("x.y as foo.bar")
		}, `select "x"."y" as "foo.bar" from "baz"`},
	})
}

// TestAliasWrappingWithSpacesInDatabaseName ports Laravel's testAliasWrappingWithSpacesInDatabaseName.
// corpus L113: select('w x.y.z as foo.bar')->from('baz').
func TestAliasWrappingWithSpacesInDatabaseName(t *testing.T) {
	runLaravelCases(t, []laravelCase{
		{"spaces in database name", func(b *Builder) *Builder {
			return b.From("baz", "").Select("w x.y.z as foo.bar")
		}, `select "w x"."y"."z" as "foo.bar" from "baz"`},
	})
}

// TestAddingSelects ports Laravel's testAddingSelects.
// corpus L120: select('foo')->addSelect('bar')->addSelect(['baz', 'boom'])->addSelect('bar').
// The corpus passes the second batch as an array; our AddSelect is variadic, so it is spelled as
// AddSelect("baz", "boom"). Both Laravel's addSelect and ours drop the repeated 'bar'.
func TestAddingSelects(t *testing.T) {
	runLaravelCases(t, []laravelCase{
		{"adding selects", func(b *Builder) *Builder {
			return b.From("users", "").Select("foo").AddSelect("bar").AddSelect("baz", "boom").AddSelect("bar")
		}, `select "foo", "bar", "baz", "boom" from "users"`},
	})
}

// TestBasicSelectWithPrefix ports Laravel's testBasicSelectWithPrefix.
// corpus L127: getBuilder(prefix: 'prefix_')->select('*')->from('users') => select * from "prefix_users".
func TestBasicSelectWithPrefix(t *testing.T) {
	b := NewBuilder(NewBaseGrammar("prefix_")).From("users", "").Select("*")
	if got := b.ToSQL(); got != `select * from "prefix_users"` {
		t.Fatalf("got %q", got)
	}
}

// TestBasicSelectDistinct ports Laravel's testBasicSelectDistinct.
// corpus L134: distinct()->select('foo', 'bar')->from('users').
func TestBasicSelectDistinct(t *testing.T) {
	runLaravelCases(t, []laravelCase{
		{"distinct two columns", func(b *Builder) *Builder {
			return b.From("users", "").Distinct().Select("foo", "bar")
		}, `select distinct "foo", "bar" from "users"`},
	})
}

// TestBasicSelectDistinctOnColumns ports Laravel's testBasicSelectDistinctOnColumns.
// corpus L141 base and L145 pgsql: distinct('foo')->select('foo', 'bar')->from('users').
func TestBasicSelectDistinctOnColumns(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"base ignores distinct columns", NewBaseGrammar(""), func(b *Builder) *Builder {
			return b.From("users", "").Distinct("foo").Select("foo", "bar")
		}, `select distinct "foo", "bar" from "users"`},
		{"postgres distinct on", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.From("users", "").Distinct("foo").Select("foo", "bar")
		}, `select distinct on ("foo") "foo", "bar" from "users"`},
	})
}

func TestDistinctReplacesPreviousColumns(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"bare distinct clears the distinct-on columns", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.From("users", "").Select("name", "id").Distinct("name").Distinct()
		}, `select distinct "name", "id" from "users"`},
		{"true clears the distinct-on columns", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.From("users", "").Select("name", "id").Distinct("name").Distinct(true)
		}, `select distinct "name", "id" from "users"`},
		{"false clears the distinct-on columns", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.From("users", "").Select("name", "id").Distinct("name").Distinct(false)
		}, `select "name", "id" from "users"`},
		{"new columns replace the distinct-on columns", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.From("users", "").Select("name", "id").Distinct("name").Distinct("id")
		}, `select distinct on ("id") "name", "id" from "users"`},
	})
}

// TestBasicAlias ports Laravel's testBasicAlias.
// corpus L152: select('foo as bar')->from('users') => select "foo" as "bar" from "users".
func TestBasicAlias(t *testing.T) {
	runLaravelCases(t, []laravelCase{
		{"basic alias", func(b *Builder) *Builder {
			return b.From("users", "").Select("foo as bar")
		}, `select "foo" as "bar" from "users"`},
	})
}

// TestAliasWithPrefix ports Laravel's testAliasWithPrefix.
// corpus L159: getBuilder(prefix: 'prefix_')->select('*')->from('users as people') prefixes both the
// table and its alias.
func TestAliasWithPrefix(t *testing.T) {
	b := NewBuilder(NewBaseGrammar("prefix_")).From("users as people", "").Select("*")
	if got := b.ToSQL(); got != `select * from "prefix_users" as "prefix_people"` {
		t.Fatalf("got %q", got)
	}
}

// TestBasicTableWrapping ports Laravel's testBasicTableWrapping.
// corpus L173: select('*')->from('public.users') => select * from "public"."users".
func TestBasicTableWrapping(t *testing.T) {
	runLaravelCases(t, []laravelCase{
		{"dotted table name", func(b *Builder) *Builder {
			return b.From("public.users", "").Select("*")
		}, `select * from "public"."users"`},
	})
}

// TestMySqlWrappingProtectsQuotationMarks ports Laravel's testMySqlWrappingProtectsQuotationMarks.
// corpus L374: getMySqlBuilder()->select('*')->From('some`table'). The embedded backtick is doubled.
func TestMySqlWrappingProtectsQuotationMarks(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"doubled backtick in table name", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.From("some`table", "").Select("*")
		}, "select * from `some``table`"},
	})
}

// TestRawExpressionsInSelect ports Laravel's testRawExpressionsInSelect.
// corpus L3520: select(new Raw('substr(foo, 6)'))->from('users').
func TestRawExpressionsInSelect(t *testing.T) {
	runLaravelCases(t, []laravelCase{
		{"raw in select", func(b *Builder) *Builder {
			return b.From("users", "").Select(Raw("substr(foo, 6)"))
		}, `select substr(foo, 6) from "users"`},
	})
}
