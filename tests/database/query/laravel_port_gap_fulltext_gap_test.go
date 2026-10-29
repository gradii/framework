package query

import (
	"testing"

	. "github.com/gradii/framework/database/query"

	"github.com/gradii/framework/database/query/core"
	"github.com/stretchr/testify/assert"
)

// This file ports the corpus methods the port plan classifies as PORTABLE with area "fulltext-gap"
// (see .claude/laravel-oracle/port_plan.md): testWhereFulltextMySql and testWhereFulltextPostgres.
// The corpus is tests/Database/DatabaseQueryBuilderTest.php from laravel/framework 12.x
// (commit 0fc645c6737626a1b1636cd234192b0d78b76569), lines 1496-1566. Both chains start from a
// dialect builder (getMySqlBuilderWithProcessor / getPostgresBuilderWithProcessor), so the dialect
// identifier quoting applies: a backtick pair for MySQL and a double quote for Postgres.
//
// The options are where the port has to translate rather than copy: Laravel passes a PHP associative
// array (['mode' => 'boolean', 'expanded' => true]) while our WhereFullText takes a flat []string, in
// which a pair is spelled "mode:boolean" and a bare key is the true flag ("expanded"). Every option
// combination below is the corpus one written in that spelling, not a different case.

// TestWhereFulltextMySql ports Laravel's testWhereFulltextMySql (corpus L1496-1523): five assertions on
// select('*')->from('users')->whereFullText(...) over the MySQL builder.
func TestWhereFulltextMySql(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"natural language mode", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereFullText("body", "Hello World", nil, core.BooleanAnd)
		}, "select * from `users` where match (`body`) against (? in natural language mode)"},
		{"with query expansion", NewMySqlGrammar(""), func(b *Builder) *Builder {
			// Corpus ['expanded' => true]; the bare key is the true flag here.
			return b.Select("*").From("users", "").WhereFullText("body", "Hello World", []string{"expanded"}, core.BooleanAnd)
		}, "select * from `users` where match (`body`) against (? in natural language mode with query expansion)"},
		{"boolean mode", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereFullText("body", "+Hello -World", []string{"mode:boolean"}, core.BooleanAnd)
		}, "select * from `users` where match (`body`) against (? in boolean mode)"},
		{"boolean mode ignores expansion", NewMySqlGrammar(""), func(b *Builder) *Builder {
			// Corpus ['mode' => 'boolean', 'expanded' => true]: boolean mode wins and the expansion
			// suffix is dropped, which is what the corpus asserts.
			return b.Select("*").From("users", "").WhereFullText("body", "+Hello -World", []string{"mode:boolean", "expanded"}, core.BooleanAnd)
		}, "select * from `users` where match (`body`) against (? in boolean mode)"},
		{"several columns", NewMySqlGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereFullText([]any{"body", "title"}, "Car,Plane", nil, core.BooleanAnd)
		}, "select * from `users` where match (`body`, `title`) against (? in natural language mode)"},
	})

	// The search term is the only binding in every shape; the mode is spelled into the SQL.
	for _, value := range []string{"Hello World", "+Hello -World", "Car,Plane"} {
		builder := NewBuilder(NewMySqlGrammar("")).Select("*").From("users", "").WhereFullText("body", value, nil, core.BooleanAnd)

		assert.Equal(t, []any{value}, builder.GetBindings())
	}
}

// TestWhereFulltextPostgres ports Laravel's testWhereFulltextPostgres (corpus L1524-1566): eight
// assertions on select('*')->from('users')->whereFullText(...) over the Postgres builder.
func TestWhereFulltextPostgres(t *testing.T) {
	runDialectCases(t, []dialectCase{
		{"default language and mode", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereFullText("body", "Hello World", nil, core.BooleanAnd)
		}, `select * from "users" where (to_tsvector('english', "body")) @@ plainto_tsquery('english', ?)`},
		{"simple language", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereFullText("body", "Hello World", []string{"language:simple"}, core.BooleanAnd)
		}, `select * from "users" where (to_tsvector('simple', "body")) @@ plainto_tsquery('simple', ?)`},
		{"plain mode is the default", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereFullText("body", "Hello World", []string{"mode:plain"}, core.BooleanAnd)
		}, `select * from "users" where (to_tsvector('english', "body")) @@ plainto_tsquery('english', ?)`},
		{"phrase mode", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereFullText("body", "Hello World", []string{"mode:phrase"}, core.BooleanAnd)
		}, `select * from "users" where (to_tsvector('english', "body")) @@ phraseto_tsquery('english', ?)`},
		{"websearch mode", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereFullText("body", "+Hello -World", []string{"mode:websearch"}, core.BooleanAnd)
		}, `select * from "users" where (to_tsvector('english', "body")) @@ websearch_to_tsquery('english', ?)`},
		{"simple language with plain mode", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereFullText("body", "Hello World", []string{"language:simple", "mode:plain"}, core.BooleanAnd)
		}, `select * from "users" where (to_tsvector('simple', "body")) @@ plainto_tsquery('simple', ?)`},
		{"several columns", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereFullText([]any{"body", "title"}, "Car Plane", nil, core.BooleanAnd)
		}, `select * from "users" where (to_tsvector('english', "body") || to_tsvector('english', "title")) @@ plainto_tsquery('english', ?)`},
		{"raw mode over several columns", NewPostgresGrammar(""), func(b *Builder) *Builder {
			return b.Select("*").From("users", "").WhereFullText([]any{"body", "title"}, "Air | Plan:* -Car", []string{"mode:raw"}, core.BooleanAnd)
		}, `select * from "users" where (to_tsvector('english', "body") || to_tsvector('english', "title")) @@ to_tsquery('english', ?)`},
	})

	for _, value := range []string{"Hello World", "+Hello -World", "Car Plane", "Air | Plan:* -Car"} {
		builder := NewBuilder(NewPostgresGrammar("")).Select("*").From("users", "").WhereFullText("body", value, nil, core.BooleanAnd)

		assert.Equal(t, []any{value}, builder.GetBindings())
	}
}
