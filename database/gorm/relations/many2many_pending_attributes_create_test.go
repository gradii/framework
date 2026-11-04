package relations_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	databasegorm "github.com/gradii/framework/database/gorm"
)

// This file pins the round-10 item relation-pending-attributes (candidate 34) on the relation
// create path: (i) a relation's withAttributes-recorded pending attributes must reach the related
// INSERT through create(), and (ii) a withPivotValue pair declared on the relation (PivotValues)
// must stamp the pivot INSERT. Both mirror illuminate/database v12.55.1, executed against sqlite
// (/tmp/r10/pending_create_probe.php):
//
//	M1  metaTags()->create(['name' => 'long article'])
//	      -> insert into "with_attributes_tags" ("visible","name","updated_at","created_at")
//	         values (true,'long article',...)   [pending visible lands]
//	      -> insert into "with_attributes_pivot" ("post_id","tag_id","type") values (1,1,'meta')
//	M2  caller ['visible' => false] wins over the pending true (array_merge($pending, $attributes))
//	M7  stampedChildren()->create(['name' => 'kid'])  [HasOneOrMany::create]
//	      -> insert into "with_attributes_children" ("name","post_id","flag",...) values ('kid',1,true,...)
//	         [setForeignAttributesForCreate's pending loop, HasOneOrMany.php:498 — create AND save]
//
// The fixtures declare the pairs the way the corpus does (Laravel: BelongsToMany.php:110/481): the
// pending attribute rides OnQuery (Query.WithAttributes records it on the relation query), the
// pivot pair rides Many2Many.PivotValues. The relation object seeds its call-time pivotValues from
// the declaration, so the same write plumbing a call-time WithPivotValue feeds carries it.

// --- fixtures ----------------------------------------------------------------------------------

type mmStampedUser struct {
	ID uint `gorm:"column:id;primaryKey"`
}

func (mmStampedUser) TableName() string { return "mm_stamped_users" }

func (mmStampedUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Roles": contractsorm.Many2Many{
			Related:         &mmStampedRole{},
			Table:           "mm_stamped_pivot",
			ForeignPivotKey: "user_id",
			RelatedPivotKey: "role_id",
			OnQuery: func(query contractsorm.Query) contractsorm.Query {
				return query.WithAttributes("name", "stamped")
			},
			PivotValues: []contractsorm.PivotValue{{Column: "kind", Value: "primary"}},
		},
	}
}

type mmStampedRole struct {
	ID   uint   `gorm:"column:id;primaryKey"`
	Name string `gorm:"column:name"`
}

func (mmStampedRole) TableName() string { return "mm_stamped_roles" }

type mmStampedPost struct {
	ID uint `gorm:"column:id;primaryKey"`
}

func (mmStampedPost) TableName() string { return "mm_stamped_posts" }

func (mmStampedPost) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Children": contractsorm.HasMany{
			Related:    &mmStampedChild{},
			ForeignKey: "post_id",
			OnQuery: func(query contractsorm.Query) contractsorm.Query {
				return query.WithAttributes("flag", true)
			},
		},
	}
}

type mmStampedChild struct {
	ID     uint `gorm:"column:id;primaryKey"`
	PostID uint `gorm:"column:post_id"`
	Flag   bool `gorm:"column:flag"`
}

func (mmStampedChild) TableName() string { return "mm_stamped_children" }

func mmStampedRolesRelation(t *testing.T, builder *databasegorm.OrmBuilder, user *mmStampedUser) contractsorm.BelongsToManyRelation {
	t.Helper()

	relation, ok := builder.Related(user, "Roles").(contractsorm.BelongsToManyRelation)
	require.True(t, ok, "Related must return a many-to-many relation object")

	return relation
}

// mmInsertsFor filters the recorded statements down to the INSERTs for one table.
func mmInsertsFor(statements []recordedStatement, table string) []recordedStatement {
	found := make([]recordedStatement, 0, 1)
	for _, statement := range statements {
		if strings.HasPrefix(statement.query, `insert into "`+table+`"`) {
			found = append(found, statement)
		}
	}

	return found
}

// TestMany2Many_PendingAttributesAndDeclaredPivotValueLandOnCreate pins both halves on one write:
// the relation's pending attribute (OnQuery WithAttributes) reaches the related INSERT and the
// declared pivot pair (PivotValues) reaches the pivot INSERT.
func TestMany2Many_PendingAttributesAndDeclaredPivotValueLandOnCreate(t *testing.T) {
	builder, recorder := newKeyedRelationTestBuilder(t)

	require.NoError(t, mmStampedRolesRelation(t, builder, &mmStampedUser{ID: 7}).Create(&mmStampedRole{}))

	statements := recorder.take()

	related := mmInsertsFor(statements, "mm_stamped_roles")
	require.Len(t, related, 1)
	assert.Equal(t, `insert into "mm_stamped_roles" ("name") values (?)`, related[0].query)
	require.Len(t, related[0].args, 1)
	assert.Equal(t, "stamped", deref(related[0].args[0]))

	pivot := mmInsertsFor(statements, "mm_stamped_pivot")
	require.Len(t, pivot, 1)
	assert.Equal(t, `insert into "mm_stamped_pivot" ("kind", "role_id", "user_id") values (?, ?, ?)`, pivot[0].query)
	require.Len(t, pivot[0].args, 3)
	assert.Equal(t, "primary", deref(pivot[0].args[0]))
	assert.EqualValues(t, 42, deref(pivot[0].args[1]))
	assert.EqualValues(t, 7, deref(pivot[0].args[2]))
}

// TestMany2Many_DeclaredPivotValuesStampAttach pins the write half on the plain attach path: the
// declaration's pair rides every pivot INSERT the attach family writes (baseAttachRecord's values
// loop, IWPT :454-472), exactly as a call-time WithPivotValue's pair does.
func TestMany2Many_DeclaredPivotValuesStampAttach(t *testing.T) {
	builder, recorder := newKeyedRelationTestBuilder(t)

	require.NoError(t, mmStampedRolesRelation(t, builder, &mmStampedUser{ID: 7}).Attach([]any{uint(9)}))

	insert := mmInsertsFor(recorder.take(), "mm_stamped_pivot")
	require.Len(t, insert, 1)
	assert.Equal(t, `insert into "mm_stamped_pivot" ("kind", "role_id", "user_id") values (?, ?, ?)`, insert[0].query)
	require.Len(t, insert[0].args, 3)
	assert.Equal(t, "primary", deref(insert[0].args[0]))
	assert.EqualValues(t, 9, deref(insert[0].args[1]))
	assert.EqualValues(t, 7, deref(insert[0].args[2]))
}

// TestHasMany_PendingAttributesLandOnCreate pins the HasOneOrMany create arm (oracle M7): the
// HasOneOrMany family merges the pending attributes through setForeignAttributesForCreate's pending
// loop too, so a HasMany create inserts the pending column beside the foreign key.
func TestHasMany_PendingAttributesLandOnCreate(t *testing.T) {
	builder, recorder := newKeyedRelationTestBuilder(t)

	relation, ok := builder.Related(&mmStampedPost{ID: 7}, "Children").(contractsorm.HasOneOrManyRelation)
	require.True(t, ok, "Related must return a has-one-or-many relation object")
	require.NoError(t, relation.Create(&mmStampedChild{}))

	insert := mmInsertsFor(recorder.take(), "mm_stamped_children")
	require.Len(t, insert, 1)
	assert.Equal(t, `insert into "mm_stamped_children" ("flag", "post_id") values (?, ?)`, insert[0].query)
	require.Len(t, insert[0].args, 2)
	assert.EqualValues(t, true, deref(insert[0].args[0]))
	assert.EqualValues(t, 7, deref(insert[0].args[1]))
}
