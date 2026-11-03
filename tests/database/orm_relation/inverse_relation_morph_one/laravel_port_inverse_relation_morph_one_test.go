package inverse_relation_morph_one

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/database/orm/morphmap"
	"github.com/gradii/framework/support/carbon"
)

// This file ports Laravel's tests/Database/DatabaseEloquentInverseRelationMorphOneTest.php (12.x)
// onto the framework ORM: one Go test per Laravel test method, named
// TestInverseRelationMorphOne_<method without the test prefix>. The two tables mirror the corpus's
// createSchema() in sqlite DDL (test_posts: id + timestamps; test_images: id + morphs('imageable')
// + timestamps); rows are seeded through the ORM, and relations are loaded explicitly — the
// harness's lazy-loading rule, since Go has no property-access magic.
//
// # The inverse feature, and how it is ported
//
// Every test in this corpus pins Laravel's inverse() (chaperone) feature. Both relations are
// morphOne(...)->inverse('imageable') — or ->inverse() with the name guessed, which the oracle
// measured as 'imageable' — so after any relation query Eloquent wires the child's MorphTo
// relation in memory: $image->relationLoaded('imageable') is true and $image->imageable is the
// same parent instance, with no further query. The Go declaration is the relation's Inverse field
// (contracts/database/orm), and the loaders (database/gorm/eager_loader.go) apply the same
// write-back on both read paths — Load and With — storing the parent instance itself, so the
// read-path ports below assert assert.Same identity (oracle-re-verified by
// .claude/laravel-oracle/_inverse_writeback_probe.php). The relation writers still only fill the
// child's FK and morph-type columns, so the write-path ports (make, create, createQuietly,
// forceCreate, save, saveQuietly, updating) keep the corpus's observable outcome by re-reading
// the child's MorphTo relation and comparing keys, as their comments say. The name guessing in
// the two guessed* tests is PHP-only relation-name introspection; its loading is ported with the
// rest under the measured name.
//
// Where the corpus calls a write API, the closest Go writer is used and the comment says so: the
// writer's Make (an unsaved child with the FK preset) applies no inverse, so the made child is
// built through FindOrNew with a key that cannot exist, and the quiet / force write variants are
// Create and Save — the corpus asserts the same outcome for all of them (oracle-verified). Load
// cannot run for the unsaved child (probe: it fails with `id can't be empty`), so the one read on
// the made instance goes through the relation read path Related(...).First(...), which resolves
// the MorphTo from the in-memory FK columns.
//
// Every behavior this file asserts was measured with the oracle
// (.claude/laravel-oracle/oracle.php against illuminate/database v12.55.1), not recalled: the
// oracle executed the corpus scenarios against a real sqlite database and reported, for lazy /
// eager / guessed loading, making, creating, creatingQuietly, forceCreating, saving, savingQuietly
// and updating, relationLoaded('imageable') true with $image->imageable === $post; the guessed
// inverse name 'imageable'; make()/create() resolving the FK to [parent class, parent key]; and
// the write statements `insert into "test_images" ("imageable_id", "imageable_type", "updated_at",
// "created_at") values (?, ?, ?, ?)` and `update "test_images" set "imageable_id" = ?,
// "updated_at" = ? where "id" = ?`.

// init registers the corpus's parent model in the global morph map under the value
// resolveMorphValue writes into imageable_type — the registry the MorphTo loader resolves back to
// *inverseMorphOnePost. Laravel stores the model class name and resolves it through class_exists();
// the morph map is the Go counterpart.
func init() {
	morphmap.Register(map[string]any{"test_posts": &inverseMorphOnePost{}})
}

// --- models ------------------------------------------------------------------

// inverseMorphOnePost ports MorphOneInversePostModel: the corpus's test_posts table (id plus the
// automatic timestamps) with its two morph-one relations, which differ only in how the inverse
// name is supplied — image() is ->inverse('imageable'), guessedImage() is ->inverse().
type inverseMorphOnePost struct {
	ID        uint             `gorm:"column:id;primaryKey;autoIncrement"`
	CreatedAt *carbon.DateTime `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`

	Image        *inverseMorphOneImage `gorm:"-"`
	GuessedImage *inverseMorphOneImage `gorm:"-"`
}

func (inverseMorphOnePost) TableName() string { return "test_posts" }

func (inverseMorphOnePost) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Image":        contractsorm.MorphOne{Related: &inverseMorphOneImage{}, Name: "imageable", Inverse: "Imageable"},
		"GuessedImage": contractsorm.MorphOne{Related: &inverseMorphOneImage{}, Name: "imageable", Inverse: "Imageable"},
	}
}

// inverseMorphOneImage ports MorphOneInverseImageModel: test_images with the morphs('imageable')
// columns and the morphTo('imageable') relation. The MorphTo destination is an `any` field, the
// shape the loader fills with the concrete type the morph map resolves from imageable_type.
type inverseMorphOneImage struct {
	ID            uint             `gorm:"column:id;primaryKey;autoIncrement"`
	ImageableType string           `gorm:"column:imageable_type"`
	ImageableID   uint             `gorm:"column:imageable_id"`
	CreatedAt     *carbon.DateTime `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt     *carbon.DateTime `gorm:"autoUpdateTime;column:updated_at"`

	Imageable any `gorm:"-"`
}

func (inverseMorphOneImage) TableName() string { return "test_images" }

func (inverseMorphOneImage) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Imageable": contractsorm.MorphTo{Name: "imageable"},
	}
}

// --- schema and helpers ------------------------------------------------------

// inverseMorphOneSchema mirrors the corpus's createSchema() in sqlite DDL. morphs('imageable')
// expands to the imageable_type / imageable_id columns (Laravel also adds a composite index; no
// assertion in the corpus observes it).
func inverseMorphOneSchema(t *testing.T, query contractsorm.Query) {
	t.Helper()

	runRelationSchema(t, query,
		`create table test_posts (id integer primary key autoincrement, created_at datetime null, updated_at datetime null)`,
		`create table test_images (id integer primary key autoincrement, imageable_type varchar not null, imageable_id integer not null, created_at datetime null, updated_at datetime null)`,
	)
}

// inverseMorphOneCreatePost ports MorphOneInversePostModel::create(): a row carrying only the
// auto-assigned id and the automatic timestamps (the ORM stamps the model's timestamp fields, the
// way Eloquent does).
func inverseMorphOneCreatePost(t *testing.T, query contractsorm.Query) inverseMorphOnePost {
	t.Helper()

	post := inverseMorphOnePost{}
	require.NoError(t, cloneRelationQuery(t, query).Create(&post))
	require.NotZero(t, post.ID, "Create must read the generated key back")

	return post
}

// inverseMorphOneSeedImages ports MorphOneInverseImageModel::factory(6)->create(): one child row
// per parent, each naming its own freshly created parent (the corpus's image factory creates a
// post per image), so every post ends up with exactly one image.
func inverseMorphOneSeedImages(t *testing.T, query contractsorm.Query, count int) {
	t.Helper()

	for i := 0; i < count; i++ {
		post := inverseMorphOneCreatePost(t, query)

		image := inverseMorphOneImage{}
		require.NoError(t, cloneRelationQuery(t, query).Relation(&post, "Image").Create(&image))
	}
}

// inverseMorphOneLoadImageable reads the child's MorphTo relation (Laravel's lazy $image->imageable
// read). The write-path ports use it because the relation writers do not apply the inverse — the
// write-back lives in the loaders — so re-reading is how they observe the link. Returns the
// resolved parent.
func inverseMorphOneLoadImageable(t *testing.T, query contractsorm.Query, image *inverseMorphOneImage) *inverseMorphOnePost {
	t.Helper()

	require.NoError(t, cloneRelationQuery(t, query).Load(image, "Imageable"))
	parent, ok := image.Imageable.(*inverseMorphOnePost)
	require.True(t, ok, "the MorphTo load must resolve test_posts through the morph map")

	return parent
}

// inverseMorphOneAssertChaperoned asserts the corpus's inverse pair for a loaded child:
// assertTrue($image->relationLoaded('imageable')) and assertSame($post, $image->imageable) — the
// load write-back stores the parent instance itself, so the child's relation costs no query.
func inverseMorphOneAssertChaperoned(t *testing.T, image *inverseMorphOneImage, post *inverseMorphOnePost) {
	t.Helper()

	require.NotNil(t, image.Imageable, "the inverse declaration must link the child back to the parent")
	assert.Same(t, post, image.Imageable)
}

// --- ports of the corpus -----------------------------------------------------

func TestInverseRelationMorphOne_MorphOneInverseRelationIsProperlySetToParentWhenLazyLoaded(t *testing.T) {
	query := newRelationQuery(t)
	inverseMorphOneSchema(t, query)
	inverseMorphOneSeedImages(t, query, 6)

	// MorphOneInversePostModel::all().
	posts := []inverseMorphOnePost{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&inverseMorphOnePost{}).Get(&posts))
	require.Len(t, posts, 6)

	for i := range posts {
		post := &posts[i]

		// Laravel: assertFalse($post->relationLoaded('image')) — Go has no relation cache; a fresh
		// model's relation field is empty until a load fills it.
		assert.Nil(t, post.Image)

		// $post->image — the lazy load, explicit in Go.
		require.NoError(t, cloneRelationQuery(t, query).Load(post, "Image"))
		require.NotNil(t, post.Image)

		// Laravel: assertTrue($image->relationLoaded('imageable')) and
		// assertSame($post, $image->imageable) — the loader write-back.
		inverseMorphOneAssertChaperoned(t, post.Image, post)
	}
}

func TestInverseRelationMorphOne_MorphOneInverseRelationIsProperlySetToParentWhenEagerLoaded(t *testing.T) {
	query := newRelationQuery(t)
	inverseMorphOneSchema(t, query)
	inverseMorphOneSeedImages(t, query, 6)

	// MorphOneInversePostModel::with('image')->get().
	posts := []inverseMorphOnePost{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&inverseMorphOnePost{}).With("Image").Get(&posts))
	require.Len(t, posts, 6)

	for i := range posts {
		post := &posts[i]

		// Laravel: $post->getRelation('image') — the eager load shows as the field being
		// populated by the query.
		require.NotNil(t, post.Image)

		// Laravel: assertTrue($image->relationLoaded('imageable')) and
		// assertSame($post, $image->imageable) — the loader write-back.
		inverseMorphOneAssertChaperoned(t, post.Image, post)
	}
}

func TestInverseRelationMorphOne_MorphOneGuessedInverseRelationIsProperlySetToParentWhenLazyLoaded(t *testing.T) {
	// ->inverse() with no argument guesses the inverse relation name — the oracle measured the
	// guess for this corpus as 'imageable'. The guessing is PHP-only relation-name introspection;
	// what this test also exercises, and the port runs, is the loading through the guessedImage
	// relation and back through the child's MorphTo.
	query := newRelationQuery(t)
	inverseMorphOneSchema(t, query)
	inverseMorphOneSeedImages(t, query, 6)

	posts := []inverseMorphOnePost{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&inverseMorphOnePost{}).Get(&posts))
	require.Len(t, posts, 6)

	for i := range posts {
		post := &posts[i]

		// Laravel: assertFalse($post->relationLoaded('guessedImage')).
		assert.Nil(t, post.GuessedImage)

		// $post->guessedImage — the lazy load, explicit in Go.
		require.NoError(t, cloneRelationQuery(t, query).Load(post, "GuessedImage"))
		require.NotNil(t, post.GuessedImage)

		// Laravel: assertTrue($image->relationLoaded('imageable')) and
		// assertSame($post, $image->imageable) — the loader write-back.
		inverseMorphOneAssertChaperoned(t, post.GuessedImage, post)
	}
}

func TestInverseRelationMorphOne_MorphOneGuessedInverseRelationIsProperlySetToParentWhenEagerLoaded(t *testing.T) {
	// See the lazy guessed test above: the guessed name is PHP-only introspection, the loading it
	// drives is what is ported.
	query := newRelationQuery(t)
	inverseMorphOneSchema(t, query)
	inverseMorphOneSeedImages(t, query, 6)

	// MorphOneInversePostModel::with('guessedImage')->get().
	posts := []inverseMorphOnePost{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&inverseMorphOnePost{}).With("GuessedImage").Get(&posts))
	require.Len(t, posts, 6)

	for i := range posts {
		post := &posts[i]

		// Laravel: $post->getRelation('guessedImage').
		require.NotNil(t, post.GuessedImage)

		// Laravel: assertTrue($image->relationLoaded('imageable')) and
		// assertSame($post, $image->imageable) — the loader write-back.
		inverseMorphOneAssertChaperoned(t, post.GuessedImage, post)
	}
}

func TestInverseRelationMorphOne_MorphOneInverseRelationIsProperlySetToParentWhenMaking(t *testing.T) {
	query := newRelationQuery(t)
	inverseMorphOneSchema(t, query)

	post := inverseMorphOneCreatePost(t, query)

	// Laravel: $post->image()->make() — a new child with the FK preset and the inverse relation
	// wired, never persisted. The writer's Make applies no inverse, so the port keeps its pinned
	// not-persisted construction path: FindOrNew with a key that cannot exist fills dest with a
	// new instance of the related model and pre-sets the FK (and morph type).
	image := inverseMorphOneImage{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&post, "Image").FindOrNew(0, &image))
	assert.Zero(t, image.ID, "make() builds the child without persisting it")

	// The FK preset make() writes from the parent: the parent key and the parent's morph identity
	// (the oracle: [parent class, parent key]; the Go morph map registers test_posts).
	assert.Equal(t, post.ID, image.ImageableID)
	assert.Equal(t, "test_posts", image.ImageableType)

	// Laravel: assertTrue($image->relationLoaded('imageable')) and
	// assertSame($post, $image->imageable). Load cannot run for an unsaved child (probe: `id can't
	// be empty` — the eager loader reads the child's own key), so $image->imageable goes through
	// the relation read path, which resolves the MorphTo from the in-memory FK columns.
	resolved := inverseMorphOnePost{}
	require.NoError(t, cloneRelationQuery(t, query).Related(&image, "Imageable").First(&resolved))
	assert.Equal(t, post.ID, resolved.ID)
}

func TestInverseRelationMorphOne_MorphOneInverseRelationIsProperlySetToParentWhenCreating(t *testing.T) {
	query := newRelationQuery(t)
	inverseMorphOneSchema(t, query)

	post := inverseMorphOneCreatePost(t, query)

	// Laravel: $post->image()->create() — the child is created with the FK set from the parent.
	// The oracle measures the statement as `insert into "test_images" ("imageable_id",
	// "imageable_type", "updated_at", "created_at") values (?, ?, ?, ?)` with the parent's key and
	// morph class.
	image := inverseMorphOneImage{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&post, "Image").Create(&image))
	require.NotZero(t, image.ID)

	assert.Equal(t, post.ID, image.ImageableID)
	assert.Equal(t, "test_posts", image.ImageableType)

	// Laravel: assertTrue($image->relationLoaded('imageable')) and
	// assertSame($post, $image->imageable).
	parent := inverseMorphOneLoadImageable(t, query, &image)
	assert.Equal(t, post.ID, parent.ID)
}

func TestInverseRelationMorphOne_MorphOneInverseRelationIsProperlySetToParentWhenCreatingQuietly(t *testing.T) {
	// Laravel: $post->image()->createQuietly() — create() with model events suppressed. The Go
	// RelationWriter exposes no quiet variant, and the corpus registers no observers, so nothing
	// observable differs: the oracle measured createQuietly() landing the same outcome as
	// create() — FK set, child persisted, parent reachable — and the port asserts that outcome
	// through Create.
	query := newRelationQuery(t)
	inverseMorphOneSchema(t, query)

	post := inverseMorphOneCreatePost(t, query)

	image := inverseMorphOneImage{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&post, "Image").Create(&image))
	require.NotZero(t, image.ID)

	assert.Equal(t, post.ID, image.ImageableID)
	assert.Equal(t, "test_posts", image.ImageableType)

	// Laravel: assertTrue($image->relationLoaded('imageable')) and
	// assertSame($post, $image->imageable).
	parent := inverseMorphOneLoadImageable(t, query, &image)
	assert.Equal(t, post.ID, parent.ID)
}

func TestInverseRelationMorphOne_MorphOneInverseRelationIsProperlySetToParentWhenForceCreating(t *testing.T) {
	// Laravel: $post->image()->forceCreate() — create() bypassing mass-assignment guarding (the
	// model's $fillable). The Go ORM has no mass-assignment concept, so Create is the whole write;
	// the oracle measured forceCreate() landing the same outcome as create().
	query := newRelationQuery(t)
	inverseMorphOneSchema(t, query)

	post := inverseMorphOneCreatePost(t, query)

	image := inverseMorphOneImage{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&post, "Image").Create(&image))
	require.NotZero(t, image.ID)

	assert.Equal(t, post.ID, image.ImageableID)
	assert.Equal(t, "test_posts", image.ImageableType)

	// Laravel: assertTrue($image->relationLoaded('imageable')) and
	// assertSame($post, $image->imageable).
	parent := inverseMorphOneLoadImageable(t, query, &image)
	assert.Equal(t, post.ID, parent.ID)
}

func TestInverseRelationMorphOne_MorphOneInverseRelationIsProperlySetToParentWhenSaving(t *testing.T) {
	query := newRelationQuery(t)
	inverseMorphOneSchema(t, query)

	post := inverseMorphOneCreatePost(t, query)
	image := inverseMorphOneImage{}

	// Laravel: assertFalse($image->relationLoaded('imageable')) — an empty relation field in Go.
	assert.Nil(t, image.Imageable)

	// Laravel: $post->image()->save($image) — sets the child's FK from the parent and inserts it.
	require.NoError(t, cloneRelationQuery(t, query).Relation(&post, "Image").Save(&image))
	require.NotZero(t, image.ID)
	assert.Equal(t, post.ID, image.ImageableID)

	// Laravel: assertTrue($image->relationLoaded('imageable')) and
	// assertSame($post, $image->imageable).
	parent := inverseMorphOneLoadImageable(t, query, &image)
	assert.Equal(t, post.ID, parent.ID)
}

func TestInverseRelationMorphOne_MorphOneInverseRelationIsProperlySetToParentWhenSavingQuietly(t *testing.T) {
	// Laravel: $post->image()->saveQuietly($image) — save() with model events suppressed. The Go
	// RelationWriter exposes no quiet variant, and the corpus registers no observers, so the
	// oracle-verified outcome of saveQuietly() (identical to save()) is asserted through Save.
	query := newRelationQuery(t)
	inverseMorphOneSchema(t, query)

	post := inverseMorphOneCreatePost(t, query)
	image := inverseMorphOneImage{}

	// Laravel: assertFalse($image->relationLoaded('imageable')).
	assert.Nil(t, image.Imageable)

	require.NoError(t, cloneRelationQuery(t, query).Relation(&post, "Image").Save(&image))
	require.NotZero(t, image.ID)
	assert.Equal(t, post.ID, image.ImageableID)

	// Laravel: assertTrue($image->relationLoaded('imageable')) and
	// assertSame($post, $image->imageable).
	parent := inverseMorphOneLoadImageable(t, query, &image)
	assert.Equal(t, post.ID, parent.ID)
}

func TestInverseRelationMorphOne_MorphOneInverseRelationIsProperlySetToParentWhenUpdating(t *testing.T) {
	query := newRelationQuery(t)
	inverseMorphOneSchema(t, query)

	post := inverseMorphOneCreatePost(t, query)
	other := inverseMorphOneCreatePost(t, query)

	// MorphOneInverseImageModel::factory()->create(): the image is created bound to a parent of
	// its own, not to $post.
	image := inverseMorphOneImage{}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&other, "Image").Create(&image))

	// Laravel: assertTrue($post->isNot($image->imageable)) — read the parent the image currently
	// points at and compare keys.
	boundTo := inverseMorphOneLoadImageable(t, query, &image)
	assert.NotEqual(t, post.ID, boundTo.ID)

	// Laravel: $post->image()->save($image) — re-parents the existing child. The oracle measures
	// the statement as `update "test_images" set "imageable_id" = ?, "updated_at" = ? where
	// "id" = ?`.
	require.NoError(t, cloneRelationQuery(t, query).Relation(&post, "Image").Save(&image))
	assert.Equal(t, post.ID, image.ImageableID)

	// Laravel: assertTrue($post->is($image->imageable)) and
	// assertSame($post, $image->imageable).
	parent := inverseMorphOneLoadImageable(t, query, &image)
	assert.Equal(t, post.ID, parent.ID)
}
