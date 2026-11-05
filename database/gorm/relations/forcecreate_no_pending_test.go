package relations_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
)

// This file pins candidate 41 (round 13): the ForceCreate family must NOT merge a relation's
// withAttributes pendingAttributes pair. Laravel's HasOneOrMany::forceCreate (vendor v12.55.1,
// HasOneOrMany.php:414-419) and MorphOneOrMany::forceCreate (MorphOneOrMany.php:78-84) fold only
// the foreign key (and the morph type) into the attribute array and never call
// setForeignAttributesForCreate's pending loop, so the forced INSERT omits the pending column. All
// four forced variants are covered, on the has-many, has-one and morph arms; the contrast pins
// (create / save) prove the pending pair still merges everywhere else. Oracle v12.55.1, executed
// (/tmp/r13/forcecreate_probe.php, real in-memory sqlite + Capsule):
//
//	create(hasMany)              -> insert into "fu_posts" ("title","fu_user_id","flag") values (...)  [pending merges]
//	forceCreate(hasMany)         -> insert into "fu_posts" ("title","fu_user_id") values (?, ?)        [no pending]
//	forceCreateQuietly(hasMany)  -> insert into "fu_posts" ("title","fu_user_id") values (?, ?)        [no pending]
//	forceCreateMany(hasMany)     -> insert into "fu_posts" ("title","fu_user_id") values (?, ?)        [no pending]
//	forceCreateManyQuietly(...)  -> insert into "fu_posts" ("title","fu_user_id") values (?, ?)        [no pending]
//	forceCreate(hasOne)          -> insert into "fu_phones" ("label","fu_user_id") values (?, ?)       [no pending]
//	forceCreate(morph)           -> insert into "fu_images" ("url","imageable_id","imageable_type")    [type set, no pending]
//
// The fixtures declare the pending pair the way the corpus does: the attribute rides the relation
// declaration's OnQuery (Query.WithAttributes records it on the relation query).

// --- fixtures ----------------------------------------------------------------------------------

type fcNoPendUser struct {
	ID uint `gorm:"column:id;primaryKey"`
}

func (fcNoPendUser) TableName() string { return "fc_nopend_users" }

func (fcNoPendUser) Relations() map[string]contractsorm.Relation {
	pending := func(query contractsorm.Query) contractsorm.Query {
		return query.WithAttributes("flag", true)
	}

	return map[string]contractsorm.Relation{
		"Children":  contractsorm.HasMany{Related: &fcNoPendChild{}, ForeignKey: "fc_user_id", OnQuery: pending},
		"OnlyChild": contractsorm.HasOne{Related: &fcNoPendChild{}, ForeignKey: "fc_user_id", OnQuery: pending},
		"Image":     contractsorm.MorphOne{Related: &fcNoPendImage{}, Name: "imageable", OnQuery: pending},
	}
}

type fcNoPendChild struct {
	ID       uint   `gorm:"column:id;primaryKey"`
	FcUserID uint   `gorm:"column:fc_user_id"`
	Title    string `gorm:"column:title"`
	Flag     bool   `gorm:"column:flag"`
}

func (fcNoPendChild) TableName() string { return "fc_nopend_children" }

type fcNoPendImage struct {
	ID            uint   `gorm:"column:id;primaryKey"`
	ImageableID   uint   `gorm:"column:imageable_id"`
	ImageableType string `gorm:"column:imageable_type"`
	URL           string `gorm:"column:url"`
	Flag          bool   `gorm:"column:flag"`
}

func (fcNoPendImage) TableName() string { return "fc_nopend_images" }

// fcNoPendInsert returns the single INSERT the recorded statements produced for table.
func fcNoPendInsert(t *testing.T, recorder *statementRecorder, table string) recordedStatement {
	t.Helper()

	insert := mmInsertsFor(recorder.take(), table)
	require.Len(t, insert, 1, "exactly one INSERT for %s", table)

	return insert[0]
}

// TestForceCreate_HasManyDropsPendingAttributes is the per-variant pin: each of ForceCreate,
// ForceCreateQuietly, ForceCreateMany and ForceCreateManyQuietly must compile the forced INSERT
// with only the foreign key beside the caller's own attribute — the pending flag column and its
// value are absent (with the pending merge restored the INSERT would carry three columns / args).
func TestForceCreate_HasManyDropsPendingAttributes(t *testing.T) {
	cases := []struct {
		name string
		call func(relation contractsorm.HasOneOrManyRelation) error
	}{
		{"ForceCreate", func(relation contractsorm.HasOneOrManyRelation) error {
			return relation.ForceCreate(&fcNoPendChild{Title: "t"})
		}},
		{"ForceCreateQuietly", func(relation contractsorm.HasOneOrManyRelation) error {
			return relation.ForceCreateQuietly(&fcNoPendChild{Title: "t"})
		}},
		{"ForceCreateMany", func(relation contractsorm.HasOneOrManyRelation) error {
			return relation.ForceCreateMany([]*fcNoPendChild{{Title: "t"}})
		}},
		{"ForceCreateManyQuietly", func(relation contractsorm.HasOneOrManyRelation) error {
			return relation.ForceCreateManyQuietly([]*fcNoPendChild{{Title: "t"}})
		}},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			builder, recorder := newKeyedRelationTestBuilder(t)

			relation, ok := builder.Related(&fcNoPendUser{ID: 7}, "Children").(contractsorm.HasOneOrManyRelation)
			require.True(t, ok, "Related must return a has-one-or-many relation object")

			require.NoError(t, testCase.call(relation))

			insert := fcNoPendInsert(t, recorder, "fc_nopend_children")
			assert.NotContains(t, insert.query, `"flag"`, "the forced INSERT must not carry the pending column")
			require.Len(t, insert.args, 2, "only the caller's attribute and the foreign key")
			assert.ElementsMatch(t, []any{"t", int64(7)}, fcNoPendValues(insert.args))
		})
	}
}

// TestForceCreate_HasOneDropsPendingAttributes pins the has-one arm of the forced path.
func TestForceCreate_HasOneDropsPendingAttributes(t *testing.T) {
	builder, recorder := newKeyedRelationTestBuilder(t)

	relation, ok := builder.Related(&fcNoPendUser{ID: 7}, "OnlyChild").(contractsorm.HasOneRelation)
	require.True(t, ok, "Related must return a has-one relation object")

	require.NoError(t, relation.ForceCreate(&fcNoPendChild{Title: "ho"}))

	insert := fcNoPendInsert(t, recorder, "fc_nopend_children")
	assert.NotContains(t, insert.query, `"flag"`)
	require.Len(t, insert.args, 2)
	assert.ElementsMatch(t, []any{"ho", int64(7)}, fcNoPendValues(insert.args))
}

// TestForceCreate_MorphDropsPendingAttributes pins the morph arm: the forced INSERT still stamps
// the morph type beside the foreign key, but not the pending pair.
func TestForceCreate_MorphDropsPendingAttributes(t *testing.T) {
	builder, recorder := newKeyedRelationTestBuilder(t)

	relation, ok := builder.Related(&fcNoPendUser{ID: 7}, "Image").(contractsorm.MorphOneRelation)
	require.True(t, ok, "Related must return a morph-one relation object")

	require.NoError(t, relation.ForceCreate(&fcNoPendImage{URL: "u"}))

	insert := fcNoPendInsert(t, recorder, "fc_nopend_images")
	assert.Contains(t, insert.query, `"imageable_type"`, "the forced morph INSERT still stamps the morph type")
	assert.NotContains(t, insert.query, `"flag"`, "the forced INSERT must not carry the pending column")
	require.Len(t, insert.args, 3)
	assert.ElementsMatch(t, []any{"u", int64(7), "fc_nopend_users"}, fcNoPendValues(insert.args))
}

// TestCreateAndSave_StillMergePendingAttributes is the contrast that keeps the forced pins
// meaningful: create and save keep merging the pending pair through setForeignAttributesForCreate's
// pending loop (HasOneOrMany.php:498).
func TestCreateAndSave_StillMergePendingAttributes(t *testing.T) {
	builder, recorder := newKeyedRelationTestBuilder(t)

	relation, ok := builder.Related(&fcNoPendUser{ID: 7}, "Children").(contractsorm.HasOneOrManyRelation)
	require.True(t, ok)

	require.NoError(t, relation.Create(&fcNoPendChild{Title: "c"}))

	created := fcNoPendInsert(t, recorder, "fc_nopend_children")
	assert.Contains(t, created.query, `"flag"`, "create still merges the pending pair")
	assert.ElementsMatch(t, []any{"c", int64(7), true}, fcNoPendValues(created.args))

	require.NoError(t, relation.Save(&fcNoPendChild{Title: "s"}))

	saved := fcNoPendInsert(t, recorder, "fc_nopend_children")
	assert.Contains(t, saved.query, `"flag"`, "save still merges the pending pair")
	assert.ElementsMatch(t, []any{"s", int64(7), true}, fcNoPendValues(saved.args))
}

// fcNoPendValues dereferences every binding and normalises the integer widths the driver may
// report, so a set assertion can compare across value shapes order-independently.
func fcNoPendValues(args []any) []any {
	values := make([]any, 0, len(args))
	for _, arg := range args {
		value := deref(arg)
		if integer, ok := value.(int); ok {
			value = int64(integer)
		}
		values = append(values, value)
	}

	return values
}
