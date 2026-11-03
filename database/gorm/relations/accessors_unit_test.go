package relations

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
)

// The accessor family's projection-level pins (item lalign-accessors): the HasOneOrMany and
// BelongsTo / MorphTo key accessors over literal descriptor projections, the parent-side
// createdAt / updatedAt resolution, and the BelongsToMany findSole / firstWhere composites. Every
// value was measured by the oracle (php .claude/laravel-oracle/oracle.php
// /tmp/lalign-probes/accessors.php, illuminate/database v12.55.1, sqlite); the real-descriptor
// half lives in accessors_test.go.

func TestHasOneOrMany_KeyAccessorsFromProjection(t *testing.T) {
	many := Wrap(nil, nil, "posts", RelationInfo{
		Kind:         KindHasMany,
		ParentTable:  "lax_users",
		RelatedTable: "lax_posts",
		References:   []RefKey{{PrimaryColumn: "id", ForeignColumn: "lax_user_id"}},
	}, Hooks{}).(contractsorm.HasManyRelation)

	// Oracle: $user->posts() reports "lax_user_id" / "lax_posts.lax_user_id" / "id" /
	// "lax_posts.lax_user_id" (getExistenceCompareKey == getQualifiedForeignKeyName).
	require.Equal(t, "lax_user_id", many.GetForeignKeyName())
	require.Equal(t, "lax_posts.lax_user_id", many.GetQualifiedForeignKeyName())
	require.Equal(t, "id", many.GetLocalKeyName())
	require.Equal(t, "lax_posts.lax_user_id", many.GetExistenceCompareKey())

	// The morph sibling inherits the family through the embedding chain; oracle:
	// $user->comments() reports "commentable_id" / "lax_comments.commentable_id" / "id" /
	// "lax_comments.commentable_id".
	morph := Wrap(nil, nil, "comments", RelationInfo{
		Kind:            KindMorphMany,
		ParentTable:     "lax_users",
		RelatedTable:    "lax_comments",
		MorphTypeColumn: "lax_comments.commentable_type",
		MorphValue:      "LaxUser",
		References:      []RefKey{{PrimaryColumn: "id", ForeignColumn: "commentable_id"}},
	}, Hooks{}).(contractsorm.MorphManyRelation)
	require.Equal(t, "commentable_id", morph.GetForeignKeyName())
	require.Equal(t, "lax_comments.commentable_id", morph.GetQualifiedForeignKeyName())
	require.Equal(t, "id", morph.GetLocalKeyName())
	require.Equal(t, "lax_comments.commentable_id", morph.GetExistenceCompareKey())

	// A column that already carries a qualifier passes through — Laravel's Model::qualifyColumn.
	dotted := Wrap(nil, nil, "posts", RelationInfo{
		Kind:         KindHasMany,
		RelatedTable: "lax_posts",
		References:   []RefKey{{PrimaryColumn: "id", ForeignColumn: "lax_posts.lax_user_id"}},
	}, Hooks{}).(contractsorm.HasManyRelation)
	require.Equal(t, "lax_user_id", dotted.GetForeignKeyName())
	require.Equal(t, "lax_posts.lax_user_id", dotted.GetQualifiedForeignKeyName())

	// A projection without a reference pair reports the empty names, never a panic.
	bare := Wrap(nil, nil, "posts", RelationInfo{Kind: KindHasMany}, Hooks{}).(contractsorm.HasManyRelation)
	require.Equal(t, "", bare.GetForeignKeyName())
	require.Equal(t, "", bare.GetQualifiedForeignKeyName())
	require.Equal(t, "", bare.GetLocalKeyName())
	require.Equal(t, "", bare.GetExistenceCompareKey())
}

func TestBelongsTo_KeyAccessorsFromProjection(t *testing.T) {
	rel := Wrap(nil, nil, "user", RelationInfo{
		Kind:         KindBelongsTo,
		ParentTable:  "lax_posts",
		RelatedTable: "lax_users",
		References:   []RefKey{{PrimaryColumn: "id", ForeignColumn: "lax_user_id"}},
	}, Hooks{}).(contractsorm.BelongsToRelation)

	// Oracle: $post->user() reports "lax_user_id" / "lax_posts.lax_user_id" / "id" /
	// "lax_users.id" / "user".
	require.Equal(t, "lax_user_id", rel.GetForeignKeyName())
	require.Equal(t, "lax_posts.lax_user_id", rel.GetQualifiedForeignKeyName())
	require.Equal(t, "id", rel.GetOwnerKeyName())
	require.Equal(t, "lax_users.id", rel.GetQualifiedOwnerKeyName())
	require.Equal(t, "user", rel.GetRelationName())

	// MorphTo: the morph id column and morph owner key stand in for the reference pair; oracle:
	// $comment->commentable() reports "commentable_id" / "lax_comments.commentable_id" / "id" /
	// "lax_users.id" / "commentable".
	morph := Wrap(nil, nil, "commentable", RelationInfo{
		Kind:          KindMorphTo,
		ParentTable:   "lax_comments",
		RelatedTable:  "lax_users",
		MorphIDColumn: "commentable_id",
		MorphOwnerKey: "id",
	}, Hooks{}).(contractsorm.MorphToRelation)
	require.Equal(t, "commentable_id", morph.GetForeignKeyName())
	require.Equal(t, "lax_comments.commentable_id", morph.GetQualifiedForeignKeyName())
	require.Equal(t, "id", morph.GetOwnerKeyName())
	require.Equal(t, "lax_users.id", morph.GetQualifiedOwnerKeyName())
	require.Equal(t, "commentable", morph.GetRelationName())

	// An explicit owner key; oracle: an explicit morphTo(..., 'custom_key') reports "custom_key".
	explicit := Wrap(nil, nil, "commentable", RelationInfo{
		Kind:          KindMorphTo,
		ParentTable:   "lax_comments",
		RelatedTable:  "lax_users",
		MorphIDColumn: "commentable_id",
		MorphOwnerKey: "custom_key",
	}, Hooks{}).(contractsorm.MorphToRelation)
	require.Equal(t, "custom_key", explicit.GetOwnerKeyName())
	require.Equal(t, "lax_users.custom_key", explicit.GetQualifiedOwnerKeyName())

	// The null owner key — the oracle's eager form (no type set) reports null and "" — is the
	// MorphTo override's own branch (MorphTo.php:429-437).
	nullOwner := Wrap(nil, nil, "commentable", RelationInfo{
		Kind:          KindMorphTo,
		ParentTable:   "lax_comments",
		MorphIDColumn: "commentable_id",
	}, Hooks{}).(contractsorm.MorphToRelation)
	require.Equal(t, "", nullOwner.GetOwnerKeyName())
	require.Equal(t, "", nullOwner.GetQualifiedOwnerKeyName())
}

func TestRelationQuery_CreatedAtUpdatedAt(t *testing.T) {
	parent := struct{ Name string }{Name: "parent"}

	// The hooks resolve the columns off the parent model the relation was resolved from — the
	// oracle's parent-side read (Relations/Relation.php:373/383: $user->posts() reports the user
	// class's "user_created_at", not the posts class's "post_created_at").
	resolved := Wrap(nil, &parent, "posts", RelationInfo{Kind: KindHasMany}, Hooks{
		ModelCreatedAtColumn: func(model any) (string, error) {
			require.Equal(t, &parent, model)

			return "user_created_at", nil
		},
		ModelUpdatedAtColumn: func(model any) (string, error) { return "user_updated_at", nil },
	})
	require.Equal(t, "user_created_at", resolved.CreatedAt())
	require.Equal(t, "user_updated_at", resolved.UpdatedAt())

	// No hook, an empty resolution and a failing resolution all report Laravel's
	// static::CREATED_AT / static::UPDATED_AT defaults — the oracle's $timestamps = false model
	// still reports "created_at" / "updated_at".
	plain := Wrap(nil, nil, "posts", RelationInfo{Kind: KindHasMany}, Hooks{})
	require.Equal(t, "created_at", plain.CreatedAt())
	require.Equal(t, "updated_at", plain.UpdatedAt())

	empty := Wrap(nil, nil, "posts", RelationInfo{Kind: KindHasMany}, Hooks{
		ModelCreatedAtColumn: func(model any) (string, error) { return "", nil },
		ModelUpdatedAtColumn: func(model any) (string, error) { return "", errors.New("no schema") },
	})
	require.Equal(t, "created_at", empty.CreatedAt())
	require.Equal(t, "updated_at", empty.UpdatedAt())
}

// accessorCaptureBuilder records the composed builder calls FindSole / FirstWhere forward — the
// stub stands in for *gorm.OrmBuilder; only WhereKey / Where / Sole / First are ever reached.
type accessorCaptureBuilder struct {
	Builder

	calls   []string
	ids     []any
	columns []any
	dests   []any
}

func (b *accessorCaptureBuilder) WhereKey(id any) contractsorm.Query {
	b.calls = append(b.calls, "WhereKey")
	b.ids = append(b.ids, id)

	return &accessorCaptureQuery{parent: b}
}

func (b *accessorCaptureBuilder) Where(column any, args ...any) contractsorm.Query {
	b.calls = append(b.calls, "Where")
	b.columns = append(b.columns, column)
	b.ids = append(b.ids, args...)

	return &accessorCaptureQuery{parent: b}
}

type accessorCaptureQuery struct {
	contractsorm.Query

	parent *accessorCaptureBuilder
}

func (q *accessorCaptureQuery) Sole(dest any) error {
	q.parent.calls = append(q.parent.calls, "Sole")
	q.parent.dests = append(q.parent.dests, dest)

	return nil
}

func (q *accessorCaptureQuery) First(dest any) error {
	q.parent.calls = append(q.parent.calls, "First")
	q.parent.dests = append(q.parent.dests, dest)

	return nil
}

func TestBelongsToMany_FindSoleAndFirstWhere_ComposeOnTheBuilder(t *testing.T) {
	builder := &accessorCaptureBuilder{}
	rel := Wrap(builder, nil, "roles", RelationInfo{Kind: KindMany2Many}, Hooks{}).(contractsorm.Many2ManyRelation)

	// FindSole is the review-anchored WhereKey(id)->Sole(dest) composite — Laravel's
	// findSole($id)->sole() (BelongsToMany.php:750).
	var soleDest struct{ ID uint }
	require.NoError(t, rel.FindSole(7, &soleDest))
	require.Equal(t, []string{"WhereKey", "Sole"}, builder.calls)
	require.Equal(t, []any{7}, builder.ids)
	require.Equal(t, []any{&soleDest}, builder.dests)

	builder.calls, builder.ids, builder.columns, builder.dests = nil, nil, nil, nil

	// FirstWhere is the Where(...)->First(dest) composite — Laravel's firstWhere(...)->first()
	// (BelongsToMany.php:853).
	var firstDest struct{ Name string }
	require.NoError(t, rel.FirstWhere(&firstDest, "name", "Alpha"))
	require.Equal(t, []string{"Where", "First"}, builder.calls)
	require.Equal(t, []any{"name"}, builder.columns)
	require.Equal(t, []any{"Alpha"}, builder.ids)
	require.Equal(t, []any{&firstDest}, builder.dests)
}

func TestBelongsToMany_CreatedAtUpdatedAtPreferThePivotColumns(t *testing.T) {
	parent := struct{ Name string }{Name: "parent"}
	parentHooks := Hooks{
		ModelCreatedAtColumn: func(model any) (string, error) { return "user_created_at", nil },
		ModelUpdatedAtColumn: func(model any) (string, error) { return "user_updated_at", nil },
	}

	// Both pivot columns declared — BelongsToMany::createdAt / updatedAt (BelongsToMany.php:1559/1569,
	// `$this->pivotCreatedAt ?? $this->parent->getCreatedAtColumn() ?? Model::CREATED_AT`) report
	// the pivot's own columns ahead of the parent's. Oracle v12.55.1, executed,
	// /tmp/btm-pivot-ts-probe.php C: withTimestamps('pivot_created_at', 'pivot_updated_at') reports
	// 'pivot_created_at' / 'pivot_updated_at' while the parent class declares user_created_at.
	named := Wrap(nil, &parent, "roles", RelationInfo{
		Kind:                 KindMany2Many,
		PivotTable:           "bpu_role_user",
		PivotCreatedAtColumn: "pivot_created_at",
		PivotUpdatedAtColumn: "pivot_updated_at",
	}, parentHooks).(contractsorm.BelongsToManyRelation)
	require.Equal(t, "pivot_created_at", named.CreatedAt())
	require.Equal(t, "pivot_updated_at", named.UpdatedAt())

	// No pivot timestamps declared — the parent's columns, the base Relation::createdAt resolution.
	// Oracle A (no withTimestamps) and E (withTimestamps(false)): 'user_created_at' /
	// 'user_updated_at'.
	plain := Wrap(nil, &parent, "roles", RelationInfo{
		Kind:       KindMany2Many,
		PivotTable: "bpu_role_user",
	}, parentHooks).(contractsorm.BelongsToManyRelation)
	require.Equal(t, "user_created_at", plain.CreatedAt())
	require.Equal(t, "user_updated_at", plain.UpdatedAt())

	// The two columns resolve independently — oracle D: withTimestamps('pivot_created_at') reports
	// 'pivot_created_at' for createdAt and the parent's 'user_updated_at' for updatedAt.
	mixed := Wrap(nil, &parent, "roles", RelationInfo{
		Kind:                 KindMany2Many,
		PivotTable:           "bpu_role_user",
		PivotCreatedAtColumn: "pivot_created_at",
	}, parentHooks).(contractsorm.BelongsToManyRelation)
	require.Equal(t, "pivot_created_at", mixed.CreatedAt())
	require.Equal(t, "user_updated_at", mixed.UpdatedAt())

	// Without a parent-column hook the pivot columns still win, and an undeclared pivot column
	// falls through to Laravel's static::CREATED_AT / UPDATED_AT defaults.
	bare := Wrap(nil, nil, "roles", RelationInfo{
		Kind:                 KindMany2Many,
		PivotUpdatedAtColumn: "pivot_updated_at",
	}, Hooks{}).(contractsorm.BelongsToManyRelation)
	require.Equal(t, "created_at", bare.CreatedAt())
	require.Equal(t, "pivot_updated_at", bare.UpdatedAt())

	// MorphToMany extends BelongsToMany (MorphToMany.php:18) and inherits the override through the
	// embedding chain.
	morph := Wrap(nil, &parent, "tags", RelationInfo{
		Kind:                 KindMorphToMany,
		PivotTable:           "taggables",
		MorphTypeColumn:      "taggable_type",
		PivotCreatedAtColumn: "pivot_created_at",
		PivotUpdatedAtColumn: "pivot_updated_at",
	}, parentHooks).(contractsorm.MorphToManyRelation)
	require.Equal(t, "pivot_created_at", morph.CreatedAt())
	require.Equal(t, "pivot_updated_at", morph.UpdatedAt())
}
