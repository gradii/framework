package query

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// This file ports Laravel's tests/Database/DatabaseQueryBuilderTest.php:
// testJsonWhereNullExpressionMysql and testJsonWhereNotNullExpressionMysql.
//
// MySqlGrammar::whereNull()/whereNotNull() stringify the column with "(string) $this->getValue(...)"
// before sniffing it for a JSON selector, so a raw Expression such as new Raw('items->id') still takes
// the json_extract path. Our asString() has no Expression branch and fmt.Sprint renders the struct as
// "{items->id}", which both fooled IsJsonSelector and produced a bogus `{items` identifier; the grammar
// now reads the expression's raw SQL first, via getValueString.
//
// It reuses dialectCase/runDialectCases from laravel_port_test.go, and the expectations are the corpus
// SQL verbatim, with our "?" placeholders and per-dialect identifier quoting.

// TestJsonWhereNullExpressionMysql ports Laravel's testJsonWhereNullExpressionMysql.
func TestJsonWhereNullExpressionMysql(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"json where null expression", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereNull(Raw("items->id"), BooleanAnd, false)
		}, "select * from `users` where (json_extract(`items`, '$.\"id\"') is null OR json_type(json_extract(`items`, '$.\"id\"')) = 'NULL')"},
	})

	// A raw column contributes no binding of its own.
	assert.Empty(t, NewBuilder(NewMySqlGrammar("")).Select("*").From("users", "").
		WhereNull(Raw("items->id"), BooleanAnd, false).GetBindings())
}

// TestJsonWhereNotNullExpressionMysql ports Laravel's testJsonWhereNotNullExpressionMysql.
func TestJsonWhereNotNullExpressionMysql(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"json where not null expression", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereNotNull(Raw("items->id"), BooleanAnd)
		}, "select * from `users` where (json_extract(`items`, '$.\"id\"') is not null AND json_type(json_extract(`items`, '$.\"id\"')) != 'NULL')"},
	})

	assert.Empty(t, NewBuilder(NewMySqlGrammar("")).Select("*").From("users", "").
		WhereNotNull(Raw("items->id"), BooleanAnd).GetBindings())
}
