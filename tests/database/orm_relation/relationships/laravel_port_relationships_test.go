package relationships

import (
	"testing"

	"github.com/stretchr/testify/assert"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
)

// This file ports Laravel's tests/Database/DatabaseEloquentRelationshipsTest.php (12.x) onto the
// framework ORM: one Go test per Laravel test method, named TestRelationships_<method without the
// test prefix>.
//
// The corpus is the relation-family smoke file. Its models connect to no database — the through
// tests stand on a mocked connection and everything else is a bare Model — so there is no schema
// to create and no rows to seed; the whole file observes how a model declares and builds its ten
// relations. The port therefore mirrors the declaration-level API: in the Go ORM a model's
// relation objects are the contract structs in Relations(), so Laravel's
// `assertInstanceOf(HasOne::class, $post->attachment())` becomes a dynamic-type check on the
// "Attachment" declaration, and the accessor call itself becomes Query.Related — the Go read path
// that builds the relation-scoped query — asserted to build rather than error.
//
// Four tests exercise PHP-only sugar with no Go counterpart and are skipped individually: the
// relation-factory overrides, the pending through API, the stringy through API, and the
// higher-order through API. One test (testAlwaysUnsetBelongsToRelationWhenReceivedModelId) is
// skipped with a probe-backed divergence: the Go ORM keeps no dirty/clean model state and no
// relation cache (a relation is 'loaded' only as its destination field being non-nil), and
// RelationWriter.Associate accepts only a model pointer — associating a raw id is rejected — and
// persists the parent immediately.
//
// The corpus pins no SQL string, and the SQL previews this file's resolution assertions build are
// never compared — they are our output, not the oracle's. The oracle
// (.claude/laravel-oracle/oracle.php against illuminate/database v12.55.1) was still consulted, to
// confirm the Laravel-side key names this file's comments and skips cite: the belongsTo default
// foreign key author_id, the hasOne default post_id, the default through keys of lovers() and
// contract() (first post_id, second fake_relationship_id, local and second local id), and the
// fluent and classic mechanic and project forms resolving identical keys (m_id, c_id, mechanic_id
// and car_id; p_id, e_id, pro_id and env_id — the qualified names differing only by each class's
// own table).

// relationshipFake ports the corpus's FakeRelationship: a bare model that serves as the related
// (or through) target of every relation on Post.
type relationshipFake struct {
	ID uint `gorm:"column:id;primaryKey;autoIncrement"`
}

func (relationshipFake) TableName() string { return "fake_relationships" }

// relationshipPost ports the corpus's Post. Besides the key, the struct carries exactly the
// columns the ten declarations read from the parent when a relation resolves: the belongsTo FK
// (author_id — Laravel derives it from the relation method name, which the Go ORM cannot see, so
// it is passed explicitly) and the MorphTo columns (postable_id / postable_type).
type relationshipPost struct {
	ID           uint   `gorm:"column:id;primaryKey;autoIncrement"`
	AuthorID     uint   `gorm:"column:author_id"`
	PostableID   uint   `gorm:"column:postable_id"`
	PostableType string `gorm:"column:postable_type"`
}

func (relationshipPost) TableName() string { return "posts" }

// Relations is the Go form of the corpus's ten relation accessors; each map key is the Laravel
// method name in CamelCase.
func (relationshipPost) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		// attachment(): hasOne(FakeRelationship::class).
		"Attachment": contractsorm.HasOne{Related: &relationshipFake{}},
		// author(): belongsTo(FakeRelationship::class) — Laravel's default FK is
		// snake(relation method name).'_id' = author_id (oracle-confirmed).
		"Author": contractsorm.BelongsTo{Related: &relationshipFake{}, ForeignKey: "author_id"},
		// comments(): hasMany(FakeRelationship::class).
		"Comments": contractsorm.HasMany{Related: &relationshipFake{}},
		// likes(): morphMany(FakeRelationship::class, 'actionable').
		"Likes": contractsorm.MorphMany{Related: &relationshipFake{}, Name: "actionable"},
		// owner(): morphOne(FakeRelationship::class, 'property').
		"Owner": contractsorm.MorphOne{Related: &relationshipFake{}, Name: "property"},
		// viewers(): belongsToMany(FakeRelationship::class) — Many2Many in the Go ORM.
		"Viewers": contractsorm.Many2Many{Related: &relationshipFake{}},
		// lovers(): hasManyThrough(FakeRelationship::class, FakeRelationship::class).
		"Lovers": contractsorm.HasManyThrough{Related: &relationshipFake{}, Through: &relationshipFake{}},
		// contract(): hasOneThrough(FakeRelationship::class, FakeRelationship::class).
		"Contract": contractsorm.HasOneThrough{Related: &relationshipFake{}, Through: &relationshipFake{}},
		// tags(): morphToMany(FakeRelationship::class, 'taggable').
		"Tags": contractsorm.MorphToMany{Related: &relationshipFake{}, Name: "taggable"},
		// postable(): morphTo() — the name defaults to the calling method, so Name is explicit
		// where Laravel guesses it.
		"Postable": contractsorm.MorphTo{Name: "postable"},
	}
}

// TestRelationships_StandardRelationships ports testStandardRelationships: every relation accessor
// on Post must produce the flavour Laravel's relation factory returns. The Go oracle for the
// class is the declaration's dynamic type, and — mirroring the corpus calling $post->attachment()
// before asserting — each declaration must also resolve through Query.Related into a query
// builder, the Go form of the scope-typed relation object the accessor returns.
func TestRelationships_StandardRelationships(t *testing.T) {
	query := newRelationQuery(t)
	post := relationshipPost{}
	relations := post.Relations()

	cases := []struct {
		name string
		want any
	}{
		{"Attachment", contractsorm.HasOne{}},
		{"Author", contractsorm.BelongsTo{}},
		{"Comments", contractsorm.HasMany{}},
		{"Owner", contractsorm.MorphOne{}},
		{"Likes", contractsorm.MorphMany{}},
		{"Viewers", contractsorm.Many2Many{}},
		{"Lovers", contractsorm.HasManyThrough{}},
		{"Contract", contractsorm.HasOneThrough{}},
		{"Tags", contractsorm.MorphToMany{}},
		{"Postable", contractsorm.MorphTo{}},
	}

	for _, c := range cases {
		assert.IsType(t, c.want, relations[c.name], "the %s relation must be declared as %T", c.name, c.want)

		// The accessor call: a fresh post resolves each relation — including the self-referencing
		// through pair and the unset morph type (which resolves to a no-rows query) — into a
		// scoped builder. dest only shapes the preview; an error would surface as empty SQL.
		var dest relationshipFake
		sql := cloneRelationQuery(t, query).Related(&post, c.name).ToSql().Get(&dest)
		assert.NotEmpty(t, sql, "the %s accessor must build its relation query", c.name)
	}
}

// TestRelationships_OverriddenRelationships ports testOverriddenRelationships: a CustomPost that
// overrides the per-kind relation factories (newBelongsTo, newHasOne, ...) should make every
// accessor return its custom relation subclass.
func TestRelationships_OverriddenRelationships(t *testing.T) {
	t.Skip("PHP-only assertion (no Go equivalent): the test subclasses Post and overrides the per-kind relation factories (newBelongsTo, newHasOne, newMorphOne, ...) so every relation method returns a custom relation subclass; the Go ORM declares relations as plain contract structs in Relations() with no factory hook to override, so there is no custom relation class to observe")
}

// TestRelationships_AlwaysUnsetBelongsToRelationWhenReceivedModelId ports
// testAlwaysUnsetBelongsToRelationWhenReceivedModelId: associating a model marks the parent dirty
// and loads the relation onto it; associating a raw model id must leave the attributes clean yet
// always unset the loaded relation.
func TestRelationships_AlwaysUnsetBelongsToRelationWhenReceivedModelId(t *testing.T) {
	// Probed on the harness database: Relation(&post, "Author").Associate(&user) writes the FK into
	// the parent and persists it in place (the posts row's author_id changes in sqlite immediately;
	// the parent's relation field — when the model carries one — stays nil), while
	// Associate(uint64(1)) fails with `Related: parent must be a non-nil pointer to a struct, got
	// uint64` (int, int64, float64 and string forms report their own type the same way) and the FK
	// never changes. An explicit Load(&post, "Author") then reads the owner back, and no operation
	// ever unsets it again; LoadMissing treats a populated destination field as loaded.
	t.Skip("our ORM diverges: the Go ORM has no dirty/clean model state (no isDirty/isClean/syncOriginal) and no relation cache — a relation is 'loaded' only as its destination field being non-nil, nothing unsets that field, and RelationWriter.Associate accepts only a non-nil model pointer while persisting the parent immediately — so Laravel's associate-by-id is rejected outright and neither the dirty/clean assertions nor the 'always unset the loaded relation when the received value is a raw id' behaviour can be reproduced")
}

// TestRelationships_PendingHasThroughRelationship ports testPendingHasThroughRelationship: the
// PHP pending-through API (through($this->car())->has(...)) must resolve hasOneThrough and
// hasManyThrough relations with the same key names as the classic explicit call. The oracle
// confirms both forms resolve to the same keys (owner: local m_id, secondLocal c_id, first
// mechanic_id, fk car_id; deployments: local p_id, secondLocal e_id, first pro_id, fk env_id), but
// those names are only observable through PHP getters.
func TestRelationships_PendingHasThroughRelationship(t *testing.T) {
	t.Skip("PHP-only assertion (no Go equivalent): the test builds relations through the PHP pending-through API (through($this->car())->has(...)) and asserts they resolve to the same key names as the classic hasOneThrough/hasManyThrough call, checked exclusively through PHP-only getters (getLocalKeyName, getSecondLocalKeyName, getFirstKeyName, getForeignKeyName, getQualifiedLocalKeyName, getQualifiedFirstKeyName); the Go ORM declares through relations as structs with explicit key fields and exposes neither a pending-relation API nor key-name getters")
}

// TestRelationships_StringyHasThroughApi ports testStringyHasThroughApi: the string form of the
// fluent through API (through('car')->has('owner')) must build the same relation, with the same
// resolved keys, as the classic call.
func TestRelationships_StringyHasThroughApi(t *testing.T) {
	t.Skip("PHP-only assertion (no Go equivalent): the test asserts the string form of the fluent through API (through('car')->has('owner'), through('environments')->has('deployments')) builds the same HasOneThrough/HasManyThrough relations, with the same resolved keys, as the classic explicit call; the Go ORM has no fluent string relation definitions — through relations are declared as structs with explicit keys in Relations()")
}

// TestRelationships_HigherOrderHasThroughApi ports testHigherOrderHasThroughApi: the higher-order
// fluent form (throughCar()->hasOwner()) must build the same relation, with the same resolved
// keys, as the classic call.
func TestRelationships_HigherOrderHasThroughApi(t *testing.T) {
	t.Skip("PHP-only assertion (no Go equivalent): the test asserts the higher-order fluent form (throughCar()->hasOwner(), throughEnvironments()->hasDeployments()) builds the same HasOneThrough/HasManyThrough relations, with the same resolved keys, as the classic explicit call; the Go ORM has no higher-order method proxy — through relations are declared as structs with explicit keys in Relations()")
}
