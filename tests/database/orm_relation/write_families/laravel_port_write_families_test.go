package write_families

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	frameworkdb "github.com/gradii/framework/database/db"
)

// This file ports the quiet and forceCreate write families of Laravel's HasOneOrMany and
// BelongsToMany onto the relation objects, each pinned against the oracle (illuminate/database
// v12.55.1, executed; /tmp/lalign_write_quiet_force.php). The oracle measured, over a users ->
// posts schema:
//
//	saveQuietly        -> [] events, foreign_key 1, row persisted;  save -> saving, creating,
//	                      created, saved
//	saveManyQuietly    -> [] events, every child's foreign key 1
//	createQuietly      -> [] events, foreign_key 1;  create -> saving, creating, created, saved
//	createManyQuietly  -> [] events, two rows
//	forceCreate        -> saving, creating, created, saved, foreign_key 1
//	forceCreateQuietly -> [] events
//	forceCreateMany / forceCreateManyQuietly -> the same, per record
//	morph forceCreate  -> [] extra events beyond the model's own, imageable_id 1,
//	                      imageable_type the parent's morph class
//	BtM saveQuietly / saveManyQuietly -> [] events, pivot rows written, the model returned
//
// The framework's morph identity is the parent's table name (wf_users) — the alias-or-table
// convention the morph suite elsewhere asserts.
//
// --- models ------------------------------------------------------------------

type wfUser struct {
	ID    uint64    `gorm:"column:id;primaryKey;autoIncrement"`
	Name  string    `gorm:"column:name"`
	Posts []*wfPost `gorm:"-"`
	Phone *wfPhone  `gorm:"-"`
	Image *wfImage  `gorm:"-"`
}

func (wfUser) TableName() string { return "wf_users" }

func (wfUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Posts": contractsorm.HasMany{Related: &wfPost{}, ForeignKey: "user_id"},
		"Phone": contractsorm.HasOne{Related: &wfPhone{}, ForeignKey: "user_id"},
		"Image": contractsorm.MorphOne{Related: &wfImage{}, Name: "imageable"},
	}
}

type wfPost struct {
	ID          uint64   `gorm:"column:id;primaryKey;autoIncrement"`
	UserID      uint64   `gorm:"column:user_id"`
	Title       string   `gorm:"column:title"`
	Tags        []*wfTag `gorm:"-"`
	StrictTags  []*wfTag `gorm:"-"`
	GuardedTags []*wfTag `gorm:"-"`
}

func (wfPost) TableName() string { return "wf_posts" }

func (wfPost) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Tags":        contractsorm.Many2Many{Related: &wfTag{}, Table: "wf_post_tag", ForeignPivotKey: "post_id", RelatedPivotKey: "tag_id"},
		"StrictTags":  contractsorm.Many2Many{Related: &wfTag{}, Table: "wf_post_strict", ForeignPivotKey: "post_id", RelatedPivotKey: "tag_id"},
		"GuardedTags": contractsorm.Many2Many{Related: &wfTag{}, Table: "wf_post_guard", ForeignPivotKey: "post_id", RelatedPivotKey: "tag_id"},
	}
}

type wfPhone struct {
	ID     uint64 `gorm:"column:id;primaryKey;autoIncrement"`
	UserID uint64 `gorm:"column:user_id"`
	Name   string `gorm:"column:name"`
}

func (wfPhone) TableName() string { return "wf_phones" }

type wfImage struct {
	ID            uint64 `gorm:"column:id;primaryKey;autoIncrement"`
	ImageableID   uint64 `gorm:"column:imageable_id"`
	ImageableType string `gorm:"column:imageable_type"`
	URL           string `gorm:"column:url"`
}

func (wfImage) TableName() string { return "wf_images" }

type wfTag struct {
	ID   uint64 `gorm:"column:id;primaryKey;autoIncrement"`
	Name string `gorm:"column:name"`
}

func (wfTag) TableName() string { return "wf_tags" }

// wfPivotRow reads pivot columns back for assertions (role / note).
type wfPivotRow struct {
	PostID uint64 `gorm:"column:post_id"`
	TagID  uint64 `gorm:"column:tag_id"`
	Role   string `gorm:"column:role"`
}

func (wfPivotRow) TableName() string { return "wf_post_tag" }

// --- schema and seeding ------------------------------------------------------

func wfSchema(t *testing.T, query contractsorm.Query) {
	t.Helper()

	// wf_post_strict carries note NOT NULL with no default: an attach that omits it fails, the
	// rollback probe (oracle: syncOrFail restores [1, 2], plain sync leaves []).
	runRelationSchema(t, query,
		`create table wf_users (id integer primary key autoincrement, name text)`,
		`create table wf_posts (id integer primary key autoincrement, user_id integer, title text)`,
		`create table wf_phones (id integer primary key autoincrement, user_id integer, name text)`,
		`create table wf_images (id integer primary key autoincrement, imageable_id integer, imageable_type text, url text)`,
		`create table wf_tags (id integer primary key autoincrement, name text)`,
		`create table wf_post_tag (post_id integer, tag_id integer, role text)`,
		`create table wf_post_strict (post_id integer, tag_id integer, note text not null)`,
		`create table wf_post_guard (post_id integer, tag_id integer)`,
		`create trigger wf_post_guard_no_delete before delete on wf_post_guard begin select raise(abort, 'detach refused'); end`,
		// Candidate 41 (round 13): the pending-pair fixtures carry a nullable flag column the
		// relation declarations seed through WithAttributes("flag", true).
		`create table wf_pend_users (id integer primary key autoincrement, name text)`,
		`create table wf_pend_posts (id integer primary key autoincrement, user_id integer, title text, flag integer)`,
		`create table wf_pend_phones (id integer primary key autoincrement, user_id integer, name text, flag integer)`,
		`create table wf_pend_images (id integer primary key autoincrement, imageable_id integer, imageable_type text, url text, flag integer)`,
	)
}

func wfSeedUser(t *testing.T, query contractsorm.Query, id uint64) wfUser {
	t.Helper()

	user := wfUser{ID: id, Name: "taylor"}
	require.NoError(t, cloneRelationQuery(t, query).Model(&wfUser{}).Create(&user))

	return user
}

func wfSeedPost(t *testing.T, query contractsorm.Query, userID uint64) wfPost {
	t.Helper()

	post := wfPost{UserID: userID, Title: "post"}
	require.NoError(t, cloneRelationQuery(t, query).Model(&wfPost{}).Create(&post))

	return post
}

func wfSeedTags(t *testing.T, query contractsorm.Query, names ...string) []wfTag {
	t.Helper()

	tags := make([]wfTag, 0, len(names))
	for _, name := range names {
		tag := wfTag{Name: name}
		require.NoError(t, cloneRelationQuery(t, query).Model(&wfTag{}).Create(&tag))
		tags = append(tags, tag)
	}

	return tags
}

func wfAttachedTagIDs(t *testing.T, query contractsorm.Query, parent *wfPost, relation string) []uint64 {
	t.Helper()

	tags := []*wfTag{}
	require.NoError(t, cloneRelationQuery(t, query).Related(parent, relation).Get(&tags))

	ids := make([]uint64, 0, len(tags))
	for _, tag := range tags {
		ids = append(ids, tag.ID)
	}

	return ids
}

// wfEvents counts the model events the quiet family must suppress and the loud family must fire.
type wfEvents struct {
	creating, saving, created, saved, updating, updated int
}

func (o *wfEvents) Created(contractsorm.Event) error      { o.created++; return nil }
func (o *wfEvents) Updated(contractsorm.Event) error      { o.updated++; return nil }
func (o *wfEvents) Deleted(contractsorm.Event) error      { return nil }
func (o *wfEvents) ForceDeleted(contractsorm.Event) error { return nil }
func (o *wfEvents) Creating(contractsorm.Event) error     { o.creating++; return nil }
func (o *wfEvents) Saving(contractsorm.Event) error       { o.saving++; return nil }
func (o *wfEvents) Saved(contractsorm.Event) error        { o.saved++; return nil }
func (o *wfEvents) Updating(contractsorm.Event) error     { o.updating++; return nil }

func (o *wfEvents) quietTotal() int {
	return o.creating + o.saving + o.created + o.saved
}

func wfObserve(t *testing.T, query contractsorm.Query, model any, observer *wfEvents) {
	t.Helper()

	observable, ok := query.(contractsorm.QueryWithObserver)
	require.True(t, ok, "the ORM query must support observers")

	observable.Observe(model, observer)
}

// --- quiet family -------------------------------------------------------------

// Oracle: $user->posts()->saveQuietly($post) fires no events while saving the row with the
// foreign key set; the loud save fires saving, creating, created, saved.
func TestWriteFamilies_SaveQuietlySuppressesEvents(t *testing.T) {
	query := newRelationQuery(t)
	wfSchema(t, query)
	user := wfSeedUser(t, query, 1)

	events := &wfEvents{}
	wfObserve(t, query, &wfPost{}, events)

	relationship, ok := cloneRelationQuery(t, query).Related(&user, "Posts").(contractsorm.HasManyRelation)
	require.True(t, ok)

	post := wfPost{Title: "quiet"}
	require.NoError(t, relationship.SaveQuietly(&post))
	assert.Equal(t, uint64(1), post.UserID)
	assert.NotZero(t, post.ID)
	assert.Zero(t, events.quietTotal(), "saveQuietly fires no model events")

	loud := wfPost{Title: "loud"}
	require.NoError(t, relationship.Save(&loud))
	assert.Equal(t, 1, events.creating)
	assert.Equal(t, 1, events.saving)
	assert.Equal(t, 1, events.created)
	assert.Equal(t, 1, events.saved)

	assert.Equal(t, []uint64{post.ID, loud.ID}, wfAllPostIDs(t, query))
}

func wfAllPostIDs(t *testing.T, query contractsorm.Query) []uint64 {
	t.Helper()

	posts := []*wfPost{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&wfPost{}).OrderBy("id").Get(&posts))

	ids := make([]uint64, 0, len(posts))
	for _, post := range posts {
		ids = append(ids, post.ID)
	}

	return ids
}

// Oracle: saveManyQuietly([a, b]) fires no events and stamps both foreign keys.
func TestWriteFamilies_SaveManyQuietlySuppressesEvents(t *testing.T) {
	query := newRelationQuery(t)
	wfSchema(t, query)
	user := wfSeedUser(t, query, 1)

	events := &wfEvents{}
	wfObserve(t, query, &wfPost{}, events)

	relationship, ok := cloneRelationQuery(t, query).Related(&user, "Posts").(contractsorm.HasManyRelation)
	require.True(t, ok)

	posts := []*wfPost{{Title: "a"}, {Title: "b"}}
	require.NoError(t, relationship.SaveManyQuietly(posts))

	assert.Zero(t, events.quietTotal())
	for _, post := range posts {
		assert.NotZero(t, post.ID)
		assert.Equal(t, uint64(1), post.UserID)
	}
}

// Oracle: createQuietly(['title' => 'cq']) fires no events, persists the row and stamps the
// foreign key; the loud create fires the four events.
func TestWriteFamilies_CreateQuietlySuppressesEvents(t *testing.T) {
	query := newRelationQuery(t)
	wfSchema(t, query)
	user := wfSeedUser(t, query, 1)

	events := &wfEvents{}
	wfObserve(t, query, &wfPost{}, events)

	relationship, ok := cloneRelationQuery(t, query).Related(&user, "Posts").(contractsorm.HasManyRelation)
	require.True(t, ok)

	created := wfPost{Title: "cq"}
	require.NoError(t, relationship.CreateQuietly(&created))
	assert.Zero(t, events.quietTotal())
	assert.Equal(t, uint64(1), created.UserID)
	assert.NotZero(t, created.ID)

	loud := wfPost{Title: "cl"}
	require.NoError(t, relationship.Create(&loud))
	assert.Equal(t, 1, events.creating)
	assert.Equal(t, 1, events.saving)
	assert.Equal(t, 1, events.created)
	assert.Equal(t, 1, events.saved)
}

// Oracle: createManyQuietly fires no events and persists every row with the foreign key set.
func TestWriteFamilies_CreateManyQuietlySuppressesEvents(t *testing.T) {
	query := newRelationQuery(t)
	wfSchema(t, query)
	user := wfSeedUser(t, query, 1)

	events := &wfEvents{}
	wfObserve(t, query, &wfPost{}, events)

	relationship, ok := cloneRelationQuery(t, query).Related(&user, "Posts").(contractsorm.HasManyRelation)
	require.True(t, ok)

	posts := []*wfPost{{Title: "m1"}, {Title: "m2"}}
	require.NoError(t, relationship.CreateManyQuietly(posts))

	assert.Zero(t, events.quietTotal())
	assert.Len(t, wfAllPostIDs(t, query), 2)
	for _, post := range posts {
		assert.Equal(t, uint64(1), post.UserID)
	}
}

// --- forceCreate family -------------------------------------------------------

// Oracle: forceCreate fires the same four events, stamps the foreign key and — unlike create —
// does not merge a pending WithAttributes pair: HasOneOrMany::forceCreate (:414-419) folds only the
// key into the attribute array and never runs setForeignAttributesForCreate's pending loop; the
// morph override MorphOneOrMany.php:78-84 does the same for the morph type. The Go ForceCreate
// family now routes through ForceCreateRelation -> SetRelationFKOnChildForced on relationWriteSession,
// so it drops the pending pair the same way (see TestWriteFamilies_ForceCreateDropsPendingPair).
func TestWriteFamilies_ForceCreateFiresEventsAndStampsForeignKey(t *testing.T) {
	query := newRelationQuery(t)
	wfSchema(t, query)
	user := wfSeedUser(t, query, 1)

	events := &wfEvents{}
	wfObserve(t, query, &wfPost{}, events)

	relationship, ok := cloneRelationQuery(t, query).Related(&user, "Posts").(contractsorm.HasManyRelation)
	require.True(t, ok)

	forced := wfPost{Title: "f"}
	require.NoError(t, relationship.ForceCreate(&forced))
	assert.Equal(t, uint64(1), forced.UserID)
	assert.NotZero(t, forced.ID)
	assert.Equal(t, 1, events.creating)
	assert.Equal(t, 1, events.saving)
	assert.Equal(t, 1, events.created)
	assert.Equal(t, 1, events.saved)
}

// Oracle: forceCreateQuietly fires no events and still stamps the foreign key.
func TestWriteFamilies_ForceCreateQuietlySuppressesEvents(t *testing.T) {
	query := newRelationQuery(t)
	wfSchema(t, query)
	user := wfSeedUser(t, query, 1)

	events := &wfEvents{}
	wfObserve(t, query, &wfPost{}, events)

	relationship, ok := cloneRelationQuery(t, query).Related(&user, "Posts").(contractsorm.HasManyRelation)
	require.True(t, ok)

	forced := wfPost{Title: "fq"}
	require.NoError(t, relationship.ForceCreateQuietly(&forced))
	assert.Zero(t, events.quietTotal())
	assert.Equal(t, uint64(1), forced.UserID)
	assert.NotZero(t, forced.ID)
}

// Oracle: forceCreateMany fires the events per record; forceCreateManyQuietly fires none and both
// stamp every foreign key.
func TestWriteFamilies_ForceCreateManyAndQuietlyStampForeignKeys(t *testing.T) {
	query := newRelationQuery(t)
	wfSchema(t, query)
	user := wfSeedUser(t, query, 1)

	events := &wfEvents{}
	wfObserve(t, query, &wfPost{}, events)

	relationship, ok := cloneRelationQuery(t, query).Related(&user, "Posts").(contractsorm.HasManyRelation)
	require.True(t, ok)

	loud := []*wfPost{{Title: "fm1"}, {Title: "fm2"}}
	require.NoError(t, relationship.ForceCreateMany(loud))
	assert.Equal(t, 2, events.saving)
	assert.Equal(t, 2, events.created)

	before := events.quietTotal()
	quiet := []*wfPost{{Title: "fmq1"}}
	require.NoError(t, relationship.ForceCreateManyQuietly(quiet))
	assert.Equal(t, before, events.quietTotal(), "forceCreateManyQuietly adds no events")

	for _, post := range append(loud, quiet...) {
		assert.NotZero(t, post.ID)
		assert.Equal(t, uint64(1), post.UserID)
	}
}

// Oracle: morph forceCreate stamps imageable_id 1 and imageable_type QfUser (the parent's morph
// class; the Go convention pins the parent's table name).
func TestWriteFamilies_MorphForceCreateStampsMorphType(t *testing.T) {
	query := newRelationQuery(t)
	wfSchema(t, query)
	user := wfSeedUser(t, query, 1)

	events := &wfEvents{}
	wfObserve(t, query, &wfImage{}, events)

	relationship, ok := cloneRelationQuery(t, query).Related(&user, "Image").(contractsorm.MorphOneRelation)
	require.True(t, ok)

	image := wfImage{URL: "u"}
	require.NoError(t, relationship.ForceCreate(&image))
	assert.Equal(t, uint64(1), image.ImageableID)
	assert.Equal(t, "wf_users", image.ImageableType)
	assert.Equal(t, 1, events.creating)
	assert.Equal(t, 1, events.created)

	quiet := wfImage{URL: "q"}
	require.NoError(t, relationship.ForceCreateQuietly(&quiet))
	assert.Equal(t, uint64(1), quiet.ImageableID)
	assert.Equal(t, "wf_users", quiet.ImageableType)
	assert.Equal(t, 1, events.creating, "no extra events from forceCreateQuietly")
}

// --- BelongsToMany quiet family ----------------------------------------------

// Oracle: BtM saveQuietly fires no events while the related row and the pivot row land and the
// model is returned; the loud save fires the four events.
func TestWriteFamilies_BelongsToManySaveQuietlySuppressesEvents(t *testing.T) {
	query := newRelationQuery(t)
	wfSchema(t, query)
	user := wfSeedUser(t, query, 1)
	post := wfSeedPost(t, query, user.ID)

	events := &wfEvents{}
	wfObserve(t, query, &wfTag{}, events)

	relationship, ok := cloneRelationQuery(t, query).Related(&post, "Tags").(contractsorm.Many2ManyRelation)
	require.True(t, ok)

	tag := wfTag{Name: "t"}
	require.NoError(t, relationship.SaveQuietly(&tag))
	assert.Zero(t, events.quietTotal())
	assert.NotZero(t, tag.ID)
	assert.Equal(t, []uint64{tag.ID}, wfAttachedTagIDs(t, query, &post, "Tags"))

	loud := wfTag{Name: "l"}
	require.NoError(t, relationship.Save(&loud))
	assert.Equal(t, 1, events.creating)
	assert.Equal(t, 1, events.created)
	assert.Len(t, wfAttachedTagIDs(t, query, &post, "Tags"), 2)
}

// Oracle: BtM saveManyQuietly fires no events and writes every pivot row.
func TestWriteFamilies_BelongsToManySaveManyQuietlySuppressesEvents(t *testing.T) {
	query := newRelationQuery(t)
	wfSchema(t, query)
	user := wfSeedUser(t, query, 1)
	post := wfSeedPost(t, query, user.ID)

	events := &wfEvents{}
	wfObserve(t, query, &wfTag{}, events)

	relationship, ok := cloneRelationQuery(t, query).Related(&post, "Tags").(contractsorm.Many2ManyRelation)
	require.True(t, ok)

	// The oracle saves two brand-new models; the seed helper would fire loud creates before the
	// observer is meaningful, so the tags stay unsaved and the quiet write persists both.
	tags := []*wfTag{{Name: "t2"}, {Name: "t3"}}
	require.NoError(t, relationship.SaveManyQuietly(tags))

	assert.Zero(t, events.quietTotal())
	assert.Len(t, wfAttachedTagIDs(t, query, &post, "Tags"), 2)
}

// --- candidate 41 (round 13): the ForceCreate family drops the pending pair ---------------------

// The pending-pair fixture: these three write relations declare WithAttributes("flag", true) on the
// relation query, the way the corpus models a withAttributes() call (the attribute rides OnQuery).
type wfPendingUser struct {
	ID uint64 `gorm:"column:id;primaryKey;autoIncrement"`
}

func (wfPendingUser) TableName() string { return "wf_pend_users" }

func (wfPendingUser) Relations() map[string]contractsorm.Relation {
	pending := func(query contractsorm.Query) contractsorm.Query {
		return query.WithAttributes("flag", true)
	}

	return map[string]contractsorm.Relation{
		"Posts": contractsorm.HasMany{Related: &wfPendingPost{}, ForeignKey: "user_id", OnQuery: pending},
		"Phone": contractsorm.HasOne{Related: &wfPendingPhone{}, ForeignKey: "user_id", OnQuery: pending},
		"Image": contractsorm.MorphOne{Related: &wfPendingImage{}, Name: "imageable", OnQuery: pending},
	}
}

type wfPendingPost struct {
	ID     uint64 `gorm:"column:id;primaryKey;autoIncrement"`
	UserID uint64 `gorm:"column:user_id"`
	Title  string `gorm:"column:title"`
	Flag   bool   `gorm:"column:flag"`
}

func (wfPendingPost) TableName() string { return "wf_pend_posts" }

type wfPendingPhone struct {
	ID     uint64 `gorm:"column:id;primaryKey;autoIncrement"`
	UserID uint64 `gorm:"column:user_id"`
	Name   string `gorm:"column:name"`
	Flag   bool   `gorm:"column:flag"`
}

func (wfPendingPhone) TableName() string { return "wf_pend_phones" }

type wfPendingImage struct {
	ID            uint64 `gorm:"column:id;primaryKey;autoIncrement"`
	ImageableID   uint64 `gorm:"column:imageable_id"`
	ImageableType string `gorm:"column:imageable_type"`
	URL           string `gorm:"column:url"`
	Flag          bool   `gorm:"column:flag"`
}

func (wfPendingImage) TableName() string { return "wf_pend_images" }

// wfSeedPendingUser inserts the fixture user.
func wfSeedPendingUser(t *testing.T, query contractsorm.Query, id uint64) wfPendingUser {
	t.Helper()

	user := wfPendingUser{ID: id}
	require.NoError(t, cloneRelationQuery(t, query).Model(&wfPendingUser{}).Create(&user))

	return user
}

// wfLastInsertSQL returns the compiled SQL of the most recent INSERT in the context's query log.
func wfLastInsertSQL(t *testing.T, ctx context.Context) string {
	t.Helper()

	logs := frameworkdb.GetQueryLog(ctx)
	for index := len(logs) - 1; index >= 0; index-- {
		if strings.HasPrefix(logs[index].Query, "insert ") {
			return logs[index].Query
		}
	}
	t.Fatalf("no INSERT in the query log")

	return ""
}

// Oracle (v12.55.1, executed; /tmp/r13/forcecreate_probe.php): create merges the relation's pending
// WithAttributes pair into the related INSERT, but every member of the ForceCreate family drops it
// — HasOneOrMany::forceCreate folds only the key into the attribute array (:414-419) and the morph
// override only the key and the morph type (MorphOneOrMany.php:78-84), never running
// setForeignAttributesForCreate's pending loop. This pins the has-many (loud + quiet, single + many),
// has-one and morph arms at the SQL level.
func TestWriteFamilies_ForceCreateDropsPendingPair(t *testing.T) {
	query := newRelationQuery(t)
	wfSchema(t, query)
	user := wfSeedPendingUser(t, query, 1)

	ctx := frameworkdb.EnableQueryLog(context.Background())
	wc, ok := cloneRelationQuery(t, query).(contractsorm.QueryWithContext)
	require.True(t, ok, "the ORM query must support WithContext")

	posts, ok := wc.WithContext(ctx).Related(&user, "Posts").(contractsorm.HasManyRelation)
	require.True(t, ok)

	created := wfPendingPost{Title: "c"}
	require.NoError(t, posts.Create(&created))
	assert.Contains(t, wfLastInsertSQL(t, ctx), `"flag"`, "create merges the pending pair")

	forced := wfPendingPost{Title: "f"}
	require.NoError(t, posts.ForceCreate(&forced))
	assert.NotContains(t, wfLastInsertSQL(t, ctx), `"flag"`, "forceCreate drops the pending pair")

	quiet := wfPendingPost{Title: "q"}
	require.NoError(t, posts.ForceCreateQuietly(&quiet))
	assert.NotContains(t, wfLastInsertSQL(t, ctx), `"flag"`, "forceCreateQuietly drops the pending pair")

	require.NoError(t, posts.ForceCreateMany([]*wfPendingPost{{Title: "m"}}))
	assert.NotContains(t, wfLastInsertSQL(t, ctx), `"flag"`, "forceCreateMany drops the pending pair")

	require.NoError(t, posts.ForceCreateManyQuietly([]*wfPendingPost{{Title: "mq"}}))
	assert.NotContains(t, wfLastInsertSQL(t, ctx), `"flag"`, "forceCreateManyQuietly drops the pending pair")

	images, ok := wc.WithContext(ctx).Related(&user, "Image").(contractsorm.MorphOneRelation)
	require.True(t, ok)

	image := wfPendingImage{URL: "u"}
	require.NoError(t, images.ForceCreate(&image))
	imageSQL := wfLastInsertSQL(t, ctx)
	assert.Contains(t, imageSQL, `"imageable_type"`, "the forced morph INSERT still stamps the morph type")
	assert.NotContains(t, imageSQL, `"flag"`, "the forced morph INSERT drops the pending pair")

	phone, ok := wc.WithContext(ctx).Related(&user, "Phone").(contractsorm.HasOneRelation)
	require.True(t, ok)

	handset := wfPendingPhone{Name: "n"}
	require.NoError(t, phone.ForceCreate(&handset))
	assert.NotContains(t, wfLastInsertSQL(t, ctx), `"flag"`, "the forced has-one INSERT drops the pending pair")
}
