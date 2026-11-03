package relations_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	frameworkerrors "github.com/gradii/framework/errors"
	"github.com/gradii/framework/support/carbon"
)

// This file pins the accessor family (item lalign-accessors) against real Related() descriptors on
// the recording transport: the HasOneOrMany key accessors, the BelongsTo / MorphTo key accessors,
// the parent-side createdAt / updatedAt columns and the BelongsToMany findSole / firstWhere
// composites. Every value and statement shape was measured by the oracle
// (php .claude/laravel-oracle/oracle.php /tmp/lalign-probes/accessors.php, illuminate/database
// v12.55.1, sqlite).

// acStampUser declares both timestamp columns under custom names — the parent whose createdAt /
// updatedAt the accessors resolve from the schema (the Go form of a model class overriding
// CREATED_AT / UPDATED_AT; oracle: the parent class's constant wins over the related's).
type acStampUser struct {
	ID        uint             `gorm:"column:id;primaryKey"`
	CreatedAt *carbon.DateTime `gorm:"autoCreateTime;column:created_col"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_col"`
	Books     []*sqlBook       `gorm:"-"`
}

func (acStampUser) TableName() string { return "sql_users" }

func (acStampUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Books": contractsorm.HasMany{Related: &sqlBook{}, ForeignKey: "user_id"},
	}
}

func TestAccessors_HasOneOrManyFromRealDescriptor(t *testing.T) {
	builder, _ := newRelationTestBuilder(t)

	// Oracle: $user->posts() reports "user_id" / "lax_posts.lax_user_id" / "id" /
	// "lax_posts.lax_user_id" (getExistenceCompareKey == getQualifiedForeignKeyName).
	many, ok := builder.Related(&sqlUser{ID: 7}, "Books").(contractsorm.HasManyRelation)
	require.True(t, ok, "Related must narrow to the declared HasManyRelation")
	require.Equal(t, "user_id", many.GetForeignKeyName())
	require.Equal(t, "sql_books.user_id", many.GetQualifiedForeignKeyName())
	require.Equal(t, "id", many.GetLocalKeyName())
	require.Equal(t, "sql_books.user_id", many.GetExistenceCompareKey())

	one, ok := builder.Related(&sqlUser{ID: 7}, "Profile").(contractsorm.HasOneRelation)
	require.True(t, ok, "Related must narrow to the declared HasOneRelation")
	require.Equal(t, "user_id", one.GetForeignKeyName())
	require.Equal(t, "sql_profiles.user_id", one.GetQualifiedForeignKeyName())
	require.Equal(t, "id", one.GetLocalKeyName())
	require.Equal(t, "sql_profiles.user_id", one.GetExistenceCompareKey())

	// The morph sibling inherits the family through the embedding chain; oracle:
	// $user->comments() reports "commentable_id" / "lax_comments.commentable_id" / "id".
	morph, ok := builder.Related(&morphOneParent{ID: 9}, "Image").(contractsorm.MorphOneRelation)
	require.True(t, ok, "Related must narrow to the declared MorphOneRelation")
	require.Equal(t, "imageable_id", morph.GetForeignKeyName())
	require.Equal(t, "morph_one_images.imageable_id", morph.GetQualifiedForeignKeyName())
	require.Equal(t, "id", morph.GetLocalKeyName())
	require.Equal(t, "morph_one_images.imageable_id", morph.GetExistenceCompareKey())
}

func TestAccessors_BelongsToFromRealDescriptor(t *testing.T) {
	builder, _ := newRelationTestBuilder(t)

	// Oracle: $post->user() reports "lax_user_id" / "lax_posts.lax_user_id" / "id" /
	// "lax_users.id" / "user".
	rel, ok := builder.Related(&sqlBook{}, "Author").(contractsorm.BelongsToRelation)
	require.True(t, ok, "Related must narrow to the declared BelongsToRelation")
	require.Equal(t, "author_id", rel.GetForeignKeyName())
	require.Equal(t, "sql_books.author_id", rel.GetQualifiedForeignKeyName())
	require.Equal(t, "id", rel.GetOwnerKeyName())
	require.Equal(t, "sql_authors.id", rel.GetQualifiedOwnerKeyName())
	require.Equal(t, "Author", rel.GetRelationName())
}

func TestAccessors_MorphToFromRealDescriptor(t *testing.T) {
	registerMorphTargets(t)

	builder, _ := newRelationTestBuilder(t)

	// Oracle (instance form, type column set): $comment->commentable() reports "commentable_id" /
	// "lax_comments.commentable_id" / "id" / "lax_users.id" / "commentable"; the qualified owner
	// key names the resolved class's table.
	parent := morphToChild{ID: 1, ImageableID: 5, ImageableType: "morph_to_author"}
	rel, ok := builder.Related(&parent, "Imageable").(contractsorm.MorphToRelation)
	require.True(t, ok, "Related must narrow to the declared MorphToRelation")
	require.Equal(t, "imageable_id", rel.GetForeignKeyName())
	require.Equal(t, "morph_to_children.imageable_id", rel.GetQualifiedForeignKeyName())
	require.Equal(t, "id", rel.GetOwnerKeyName())
	require.Equal(t, "morph_to_authors.id", rel.GetQualifiedOwnerKeyName())
	require.Equal(t, "Imageable", rel.GetRelationName())

	// An explicit owner key — the descriptor pins it (descriptorFromMorphTo); oracle:
	// morphTo(..., 'custom_key') reports "custom_key". Without a resolved type there is no related
	// table to qualify the key with, so the bare key reports (the oracle's eager form reports ""
	// for the qualified name; the Go descriptor's build-time key default keeps the unqualified
	// getter on the instance form's value).
	explicit, ok := builder.Related(&morphToChild{}, "Owner").(contractsorm.MorphToRelation)
	require.True(t, ok)
	require.Equal(t, "uuid", explicit.GetOwnerKeyName())
	require.Equal(t, "uuid", explicit.GetQualifiedOwnerKeyName())
}

func TestAccessors_RelationTimestampsFromRealDescriptor(t *testing.T) {
	builder, _ := newRelationTestBuilder(t)

	// sqlUser declares only an updated-at column: the updated-at accessor resolves it from the
	// schema, and the created-at accessor reports Laravel's static::CREATED_AT default.
	rel := builder.Related(&sqlUser{ID: 7}, "Books")
	require.Equal(t, "created_at", rel.CreatedAt())
	require.Equal(t, "updated_at", rel.UpdatedAt())

	// acStampUser declares both columns under custom names — the parent's declaration wins
	// (oracle: $user->posts()->createdAt() reports the parent class's "user_created_at" while the
	// posts class declares "post_created_at").
	stamped := builder.Related(&acStampUser{ID: 7}, "Books")
	require.Equal(t, "created_col", stamped.CreatedAt())
	require.Equal(t, "updated_col", stamped.UpdatedAt())
}

func TestAccessors_TakeCompilesTheLimit(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)

	// Oracle: take(3) on an existing parent returns the relation and compiles
	// `select * from "lax_posts" where "lax_posts"."lax_user_id" = ? and
	// "lax_posts"."lax_user_id" is not null limit 3`. The Go form is the relation-level Take
	// (HasOneOrMany::take -> limit): the existing parent keeps the plain limit, the same
	// statement Limit compiles; the non-existing-parent group-limit branch is pinned in
	// take_group_limit_test.go.
	rel := builder.Related(&sqlUser{ID: 7}, "Books")
	require.NoError(t, rel.Take(3).Get(&[]sqlBook{}))

	statements := recorder.take()
	require.Len(t, statements, 1)
	assert.Equal(t, `select * from "sql_books" where "sql_books"."user_id" = ? and "sql_books"."user_id" is not null limit 3`, statements[0].query)
}

func TestAccessors_FindSoleCompilesTheSoleRead(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)

	roles, ok := builder.Related(&mmUser{ID: 7}, "Roles").(contractsorm.Many2ManyRelation)
	require.True(t, ok, "Related must narrow to the declared Many2ManyRelation")

	var sole mmRole
	err := roles.FindSole(1, &sole)
	// The recording transport answers reads with an empty result set — the Sole read's no-row
	// error, the Go form of the oracle's ModelNotFoundException (the oracle's findSole(999)
	// throws Illuminate\Database\Eloquent\ModelNotFoundException).
	require.ErrorIs(t, err, frameworkerrors.OrmRecordNotFound)

	statements := recorder.take()
	require.Len(t, statements, 1)
	// Oracle: findSole compiles the pivot-aliased statement `... where "lax_role_user"."lax_user_id"
	// = ? and "lax_roles"."id" = ? limit 2` (the sole read's two-row limit). The Go statement
	// differs only in the join rendering every relation read in this repository shares.
	assert.Equal(t, `select "mm_roles".*, "mm_user_roles"."user_id" as "pivot_user_id", "mm_user_roles"."role_id" as "pivot_role_id" from "mm_roles" inner join "mm_user_roles" ON "mm_user_roles"."role_id" = "mm_roles"."id" where "mm_user_roles"."user_id" = ? and "mm_roles"."id" = ? limit 2`, statements[0].query)
	require.Len(t, statements[0].args, 2)
	assert.EqualValues(t, 7, deref(statements[0].args[0]))
	assert.EqualValues(t, 1, deref(statements[0].args[1]))
}

func TestAccessors_FirstWhereCompilesTheWhereFirst(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)

	roles, ok := builder.Related(&mmUser{ID: 7}, "Roles").(contractsorm.Many2ManyRelation)
	require.True(t, ok, "Related must narrow to the declared Many2ManyRelation")

	var found mmRole
	require.NoError(t, roles.FirstWhere(&found, "name", "Alpha"))

	statements := recorder.take()
	require.Len(t, statements, 1)
	// Oracle: firstWhere compiles `... and "name" = ? limit 1`; a miss returns null (the Go read
	// leaves dest empty and reports no error).
	assert.Equal(t, `select "mm_roles".*, "mm_user_roles"."user_id" as "pivot_user_id", "mm_user_roles"."role_id" as "pivot_role_id" from "mm_roles" inner join "mm_user_roles" ON "mm_user_roles"."role_id" = "mm_roles"."id" where "mm_user_roles"."user_id" = ? and "name" = ? order by "mm_roles"."id" limit 1`, statements[0].query)
	require.Len(t, statements[0].args, 2)
}

// bptUser declares custom parent timestamp columns and three many-to-many relations whose pivot
// timestamp declarations differ — the BelongsToMany::createdAt / updatedAt override
// (BelongsToMany.php:1559/1569) decides between the pivot's columns and the parent's per column.
// Oracle v12.55.1, executed, /tmp/btm-pivot-ts-probe.php (the parent class declares
// CREATED_AT = 'user_created_at' / UPDATED_AT = 'user_updated_at').
type bptUser struct {
	ID           uint              `gorm:"column:id;primaryKey"`
	CreatedAt    *carbon.DateTime  `gorm:"autoCreateTime;column:user_created_at"`
	UpdatedAt    *carbon.DateTime  `gorm:"autoUpdateTime;column:user_updated_at"`
	Roles        []*bptRole        `gorm:"-"`
	StampedRoles []*bptStampedRole `gorm:"-"`
	CreatedRoles []*bptCreatedRole `gorm:"-"`
}

func (bptUser) TableName() string { return "bpt_users" }

func (bptUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		// No pivot timestamps at all — oracle A.
		"Roles": contractsorm.Many2Many{Related: &bptRole{}, Table: "bpt_role_user", ForeignPivotKey: "user_id", RelatedPivotKey: "role_id"},
		// The pivot struct names both columns — the Go form of withTimestamps('pivot_created_at',
		// 'pivot_updated_at'), oracle C.
		"StampedRoles": contractsorm.Many2Many{Related: &bptStampedRole{}, Table: "bpt_role_user", ForeignPivotKey: "user_id", RelatedPivotKey: "role_id"},
		// The pivot struct names only the created column and the PivotTimestamps fallback fills
		// the updated side from the parent — withTimestamps('pivot_created_at'), oracle D
		// (without the flag this declaration would be the two-argument
		// withTimestamps('pivot_created_at', false), which stamps only the created column).
		"CreatedRoles": contractsorm.Many2Many{Related: &bptCreatedRole{}, Table: "bpt_role_user", ForeignPivotKey: "user_id", RelatedPivotKey: "role_id", PivotTimestamps: true},
		// Bare withTimestamps(): no pivot struct names columns, so both stamps resolve the parent
		// class's columns ('user_created_at' / 'user_updated_at'), oracle B.
		"BareRoles": contractsorm.Many2Many{Related: &bptRole{}, Table: "bpt_role_user", ForeignPivotKey: "user_id", RelatedPivotKey: "role_id", PivotTimestamps: true},
	}
}

type bptRole struct {
	ID uint `gorm:"column:id;primaryKey"`
}

func (bptRole) TableName() string { return "bpt_roles" }

type bptStampedPivot struct {
	UserID  uint             `gorm:"column:user_id"`
	RoleID  uint             `gorm:"column:role_id"`
	Stamped *carbon.DateTime `gorm:"autoCreateTime;column:pivot_created_at"`
	Edited  *carbon.DateTime `gorm:"autoUpdateTime;column:pivot_updated_at"`
}

// The pivot structs carry a table name because the descriptor build parses them to resolve their
// tagged columns (ResolvePivotTimestamps -> parseSchema) and parseSchema requires a resolvable
// table.
func (bptStampedPivot) TableName() string { return "bpt_role_user" }

type bptStampedRole struct {
	ID    uint            `gorm:"column:id;primaryKey"`
	Pivot bptStampedPivot `gorm:"-"`
}

func (bptStampedRole) TableName() string { return "bpt_roles" }

type bptCreatedPivot struct {
	UserID  uint             `gorm:"column:user_id"`
	RoleID  uint             `gorm:"column:role_id"`
	Stamped *carbon.DateTime `gorm:"autoCreateTime;column:pivot_created_at"`
}

func (bptCreatedPivot) TableName() string { return "bpt_role_user" }

type bptCreatedRole struct {
	ID    uint            `gorm:"column:id;primaryKey"`
	Pivot bptCreatedPivot `gorm:"-"`
}

func (bptCreatedRole) TableName() string { return "bpt_roles" }

func TestAccessors_BelongsToManyTimestampsFromRealDescriptor(t *testing.T) {
	builder, _ := newRelationTestBuilder(t)

	// Oracle A: no withTimestamps — the parent's columns ('user_created_at' / 'user_updated_at').
	plain, ok := builder.Related(&bptUser{ID: 7}, "Roles").(contractsorm.BelongsToManyRelation)
	require.True(t, ok)
	require.Equal(t, "user_created_at", plain.CreatedAt())
	require.Equal(t, "user_updated_at", plain.UpdatedAt())

	// Oracle C: withTimestamps('pivot_created_at', 'pivot_updated_at') — the pivot's columns, as
	// resolved from the Pivot struct's tags by ResolvePivotTimestamps.
	stamped, ok := builder.Related(&bptUser{ID: 7}, "StampedRoles").(contractsorm.BelongsToManyRelation)
	require.True(t, ok)
	require.Equal(t, "pivot_created_at", stamped.CreatedAt())
	require.Equal(t, "pivot_updated_at", stamped.UpdatedAt())

	// Oracle D: withTimestamps('pivot_created_at') — created from the pivot, updated from the
	// parent.
	created, ok := builder.Related(&bptUser{ID: 7}, "CreatedRoles").(contractsorm.BelongsToManyRelation)
	require.True(t, ok)
	require.Equal(t, "pivot_created_at", created.CreatedAt())
	require.Equal(t, "user_updated_at", created.UpdatedAt())

	// Oracle B: withTimestamps() (bare) — pivotCreatedAt / pivotUpdatedAt stay null, so both
	// columns resolve the parent class's 'user_created_at' / 'user_updated_at'.
	bare, ok := builder.Related(&bptUser{ID: 7}, "BareRoles").(contractsorm.BelongsToManyRelation)
	require.True(t, ok)
	require.Equal(t, "user_created_at", bare.CreatedAt())
	require.Equal(t, "user_updated_at", bare.UpdatedAt())
}

// TestAccessors_BelongsToManyBareTimestampsWriteTheParentColumns pins the WRITE side of oracle B:
// the bare withTimestamps() pivot write stamps the parent class's columns, so the attach INSERT
// carries user_created_at / user_updated_at, and updateExistingPivot rewrites user_updated_at
// alone. Oracle v12.55.1, executed, /tmp/btm-pivot-ts-write-probe.php: bare withTimestamps()
// attaches 'user_created_at, user_updated_at'; updateExistingPivot rewrote user_updated_at and
// left the pivot's own column at its sentinel.
func TestAccessors_BelongsToManyBareTimestampsWriteTheParentColumns(t *testing.T) {
	builder, recorder := newRelationTestBuilder(t)

	require.NoError(t, builder.AttachRelation(&bptUser{ID: 7}, "BareRoles", []any{1}))

	statements := recorder.take()
	require.Len(t, statements, 1, "the attach is a single INSERT — no existence read (round 6; oracle /tmp/attach_dedup_probe.php A1a/B0)")
	assert.Equal(t, `insert into "bpt_role_user" ("role_id", "user_created_at", "user_id", "user_updated_at") values (?, ?, ?, ?)`, statements[0].query)

	_, err := builder.UpdateExistingPivotRelation(&bptUser{ID: 7}, "BareRoles", 1, map[string]any{"note": "x"})
	require.NoError(t, err)

	statements = recorder.take()
	require.Len(t, statements, 1)
	assert.Equal(t, `update "bpt_role_user" set "note" = ?, "user_updated_at" = ? where "bpt_role_user"."user_id" = ? and "bpt_role_user"."role_id" in (?)`, statements[0].query)
}
