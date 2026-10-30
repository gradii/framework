package gorm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// These tests cover oneOfManyJoin, the compiled twin of the former applyOneOfManyJoin that the
// eager loader now runs. It renders the grouped per-parent aggregate subquery that OfMany /
// LatestOfMany / OldestOfMany install on a HasOne / MorphOne eager load.
//
// The assertions are exact full statements, not substrings: oneOfManyJoin used to return an
// "INNER JOIN (...)" fragment even though conditions.join is rendered with an "inner" keyword
// prefix, so the eager OfMany path emitted "inner join INNER JOIN (...)" — invalid SQL that a
// substring check for "INNER JOIN (" happily accepted. The exact statement pins the keyword to
// exactly one occurrence and closes the byte-identity gap for this path.

// renderOfManySQL runs the same steps runRelatedQueryCompiled takes for an OfMany condition —
// build the producer's join fragment, queue it on conditions.join, and compile the statement — so
// the rendered SQL covers the JoinRaw prefixing the eager path relies on.
func renderOfManySQL(t *testing.T, desc *relationDescriptor, cfg *oneOfManyConfig) string {
	t.Helper()

	q := newRelQueryWith(t, &relUser{})
	join, args, err := q.oneOfManyJoin(desc, cfg)
	assert.NoError(t, err)

	child := newRelQueryWith(t, desc.relatedModel)
	child.query.JoinRaw(join, args, "inner")

	statement, _, err := child.compileSelect()
	assert.NoError(t, err)

	return statement
}

func TestOneOfManyJoin_HasOne_SQL(t *testing.T) {
	desc := &relationDescriptor{
		kind:         relKindHasOne,
		relatedTable: "rel_books",
		relatedModel: &relBook{},
		references: []referenceKey{{
			primaryTable:  "rel_users",
			primaryColumn: "id",
			foreignTable:  "rel_books",
			foreignColumn: "user_id",
		}},
	}
	cfg := &oneOfManyConfig{column: "id", aggregate: "MAX"}

	sql := renderOfManySQL(t, desc, cfg)

	assert.Equal(t, `select * from "rel_books" inner join (select MAX("rel_books"."id") AS aggregate, "rel_books"."user_id" from "rel_books" group by "rel_books"."user_id") goravel_one_of_many ON "rel_books"."id" = goravel_one_of_many.aggregate AND "rel_books"."user_id" = goravel_one_of_many."user_id"`, sql)
}

func TestOneOfManyJoin_MorphOne_SQL(t *testing.T) {
	desc := &relationDescriptor{
		kind:            relKindMorphOne,
		relatedTable:    "rel_logos",
		relatedModel:    &relLogo{},
		morphTypeColumn: "logoable_type",
		morphIDColumn:   "logoable_id",
		references: []referenceKey{{
			primaryTable:  "rel_users",
			primaryColumn: "id",
			foreignTable:  "rel_logos",
			foreignColumn: "logoable_id",
		}},
	}
	cfg := &oneOfManyConfig{column: "id", aggregate: "MIN"}

	sql := renderOfManySQL(t, desc, cfg)

	// The morph type column is carried through the GROUP BY and the JOIN so a morph relation
	// scopes the aggregate per owner type, not only per owner id.
	assert.Equal(t, `select * from "rel_logos" inner join (select MIN("rel_logos"."id") AS aggregate, "rel_logos"."logoable_id", "rel_logos"."logoable_type" from "rel_logos" group by "rel_logos"."logoable_id", "rel_logos"."logoable_type") goravel_one_of_many ON "rel_logos"."id" = goravel_one_of_many.aggregate AND "rel_logos"."logoable_id" = goravel_one_of_many."logoable_id" AND "rel_logos"."logoable_type" = goravel_one_of_many."logoable_type"`, sql)
}

func TestOneOfManyJoin_UnsupportedKind_NoOp(t *testing.T) {
	q := newRelQueryWith(t, &relUser{})
	desc := &relationDescriptor{
		kind:         relKindHasMany,
		relatedTable: "rel_books",
		relatedModel: &relBook{},
		references: []referenceKey{{
			primaryTable:  "rel_users",
			primaryColumn: "id",
			foreignTable:  "rel_books",
			foreignColumn: "user_id",
		}},
	}
	cfg := &oneOfManyConfig{column: "id", aggregate: "MAX"}

	join, args, err := q.oneOfManyJoin(desc, cfg)
	assert.NoError(t, err)
	assert.Nil(t, args)
	assert.Empty(t, join)
}

func TestOneOfManyJoin_NilCfg_NoOp(t *testing.T) {
	q := newRelQueryWith(t, &relUser{})
	desc := &relationDescriptor{
		kind:         relKindHasOne,
		relatedTable: "rel_books",
		relatedModel: &relBook{},
		references: []referenceKey{{
			primaryTable:  "rel_users",
			primaryColumn: "id",
			foreignTable:  "rel_books",
			foreignColumn: "user_id",
		}},
	}

	join, args, err := q.oneOfManyJoin(desc, nil)
	assert.NoError(t, err)
	assert.Nil(t, args)
	assert.Empty(t, join)
}

func TestQuery_OfManyShortcuts(t *testing.T) {
	q := newRelQueryWith(t, &relUser{})

	latest := q.LatestOfMany().(*OrmBuilder)
	assert.NotNil(t, latest.conditions.oneOfMany)
	assert.Equal(t, "MAX", latest.conditions.oneOfMany.aggregate)
	assert.Equal(t, "id", latest.conditions.oneOfMany.column)

	oldest := q.OldestOfMany("created_at").(*OrmBuilder)
	assert.NotNil(t, oldest.conditions.oneOfMany)
	assert.Equal(t, "MIN", oldest.conditions.oneOfMany.aggregate)
	assert.Equal(t, "created_at", oldest.conditions.oneOfMany.column)

	custom := q.OfMany("score", "AVG").(*OrmBuilder)
	assert.Equal(t, "AVG", custom.conditions.oneOfMany.aggregate)
	assert.Equal(t, "score", custom.conditions.oneOfMany.column)
}
