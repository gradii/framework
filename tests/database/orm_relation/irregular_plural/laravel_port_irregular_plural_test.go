package irregular_plural

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	databaseorm "github.com/gradii/framework/database/orm"
	"github.com/gradii/framework/support/carbon"
)

// This file ports Laravel's tests/Database/DatabaseEloquentIrregularPluralTest.php (12.x) onto the
// framework ORM: one Go test per Laravel test method, named
// TestIrregularPlural_<method without the test prefix>. The corpus's five tables mirror its
// createSchema() in sqlite DDL; rows are seeded through the ORM, and relation writes go through
// Relation(parent, name) — the harness's rules, since Go has no property-access magic.
//
// # What the oracle measured (oracle.php against illuminate/database v12.55.1, sqlite)
//
//   - (new IrregularPluralHuman)->getTable() is "irregular_plural_humans"
//   - the tokens relation keeps the pivot literally as the corpus declares it — table
//     irregular_plural_human_irregular_plural_token, foreignPivotKey irregular_plural_token_id,
//     relatedPivotKey irregular_plural_human_id — so the pivot's human_id column holds token ids
//     while its token_id column holds the parent human's id (sync inserts
//     `insert into "irregular_plural_human_irregular_plural_token" ("irregular_plural_human_id",
//     "irregular_plural_token_id") values (?, ?)` with [token_id, human_id])
//   - the morphToMany relation's defaults resolve to table cool_mottoes (the name "cool_motto"
//     pluralises its last underscore-separated word only — Str::plural("motto") = "mottoes" —
//     where Str::plural("cool_motto") alone gives "cool_mottos"), foreignPivotKey cool_motto_id,
//     relatedPivotKey irregular_plural_motto_id and morph type column cool_motto_type
//   - Token::$touches = ['irregularPluralHumans'] matches the guessed inverse relation
//     Str::camel(Str::pluralStudly('IrregularPluralHuman')) = "irregularPluralHumans", so sync is
//     touchingParent() and runs `update "irregular_plural_humans" set "updated_at" = ? where
//     "id" = ?` ["2018-05-01 15:16:17", 1]
//   - mottoes()->create emits `insert into "irregular_plural_mottoes" ("name") values (?)`
//     ["Real eyes realize real lies"] then `insert into "cool_mottoes" ("cool_motto_id",
//     "cool_motto_type", "irregular_plural_motto_id") values (?, ?, ?)`
//     [2, "IrregularPluralHuman", 1]
//
// # How our ORM diverges
//
//   - Table names are never derived or pluralised from the model type: a model names its table
//     only through TableName(), and a struct without one fails with the named error
//     "*irregular_plural.<Model>: Table not set, please add a TableName() method returning the
//     table name" (probed). testItPluralizesTheTableName asserts the derived name, so it is
//     skipped.

// --- models ------------------------------------------------------------------

// irregularPluralHuman ports IrregularPluralHuman. Laravel's $touches on the token model reaches
// the parent through BelongsToMany::touchingParent(); the Go ORM models the same behaviour as the
// Touches flag on the parent-side relation.
type irregularPluralHuman struct {
	databaseorm.Timestamps

	ID      uint                    `gorm:"column:id;primaryKey;autoIncrement"`
	Email   string                  `gorm:"column:email"`
	Tokens  []*irregularPluralToken `gorm:"-"`
	Mottoes []*irregularPluralMotto `gorm:"-"`
}

func (irregularPluralHuman) TableName() string { return "irregular_plural_humans" }

// MorphClass pins the class identity Laravel stores in cool_motto_type: getMorphClass() of the
// corpus's model is its class basename.
func (irregularPluralHuman) MorphClass() string { return "IrregularPluralHuman" }

// Relations declares the corpus's two relations. The tokens relation passes the pivot's foreign
// and related keys with their literal (swapped-looking) names, exactly as the corpus's
// irregularPluralTokens() does — the oracle confirms Laravel uses them as given. The mottoes
// relation declares what morphToMany(IrregularPluralMotto::class, 'cool_motto') resolves its
// defaults to.
func (irregularPluralHuman) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"IrregularPluralTokens": contractsorm.Many2Many{
			Related:         &irregularPluralToken{},
			Table:           "irregular_plural_human_irregular_plural_token",
			ForeignPivotKey: "irregular_plural_token_id",
			RelatedPivotKey: "irregular_plural_human_id",
			Touches:         true,
		},
		"Mottoes": contractsorm.MorphToMany{
			Related:         &irregularPluralMotto{},
			Name:            "cool_motto",
			Table:           "cool_mottoes",
			TypeColumn:      "cool_motto_type",
			ForeignPivotKey: "cool_motto_id",
			RelatedPivotKey: "irregular_plural_motto_id",
		},
	}
}

// irregularPluralToken ports IrregularPluralToken: no timestamps; its $touches list is what the
// Touches flag on the human's relation reproduces.
type irregularPluralToken struct {
	ID    uint   `gorm:"column:id;primaryKey;autoIncrement"`
	Title string `gorm:"column:title"`
}

func (irregularPluralToken) TableName() string { return "irregular_plural_tokens" }

// irregularPluralMotto ports IrregularPluralMotto. The corpus's morphedByMany inverse
// (irregularPluralHumans) is never exercised by a test, so the port declares no relation for it.
type irregularPluralMotto struct {
	ID   uint   `gorm:"column:id;primaryKey;autoIncrement"`
	Name string `gorm:"column:name"`
}

func (irregularPluralMotto) TableName() string { return "irregular_plural_mottoes" }

// --- schema ------------------------------------------------------------------

// irregularPluralSchema mirrors the corpus's createSchema() in sqlite DDL.
func irregularPluralSchema(t *testing.T, query contractsorm.Query) {
	t.Helper()

	runRelationSchema(t, query,
		`create table irregular_plural_humans (id integer primary key autoincrement, email varchar not null unique, created_at datetime null, updated_at datetime null)`,
		`create table irregular_plural_tokens (id integer primary key autoincrement, title varchar not null)`,
		`create table irregular_plural_human_irregular_plural_token (irregular_plural_human_id integer not null, irregular_plural_token_id integer not null)`,
		`create table irregular_plural_mottoes (id integer primary key autoincrement, name varchar not null)`,
		`create table cool_mottoes (irregular_plural_motto_id integer not null, cool_motto_id integer not null, cool_motto_type varchar not null)`,
	)
}

// --- ports of the corpus -----------------------------------------------------

func TestIrregularPlural_ItPluralizesTheTableName(t *testing.T) {
	// Laravel: (new IrregularPluralHuman)->getTable() === 'irregular_plural_humans' — the oracle
	// confirms the name Laravel derives by pluralising the class basename.
	t.Skip("our ORM diverges: the Go ORM never derives or pluralises a table name from the model type — a model addresses a table only through TableName(), and a struct without one fails with the named error `*irregular_plural.<Model>: Table not set, please add a TableName() method returning the table name` (probed), so there is no getTable() pluralisation behaviour to observe")
}

func TestIrregularPlural_ItTouchesTheParentWithAnIrregularPlural(t *testing.T) {
	query := newRelationQuery(t)
	irregularPluralSchema(t, query)

	// Carbon::setTestNow('2018-05-01 12:13:14'), IrregularPluralHuman::create(...) and
	// IrregularPluralToken::insert(...), with the clock re-frozen at 2018-05-01 15:16:17 before
	// the sync.
	carbon.SetTestNow(carbon.Parse("2018-05-01 12:13:14"))
	t.Cleanup(carbon.ClearTestNow)

	human := irregularPluralHuman{Email: "taylorotwell@gmail.com"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&human))

	token := irregularPluralToken{Title: "The title"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&token))

	reloaded := irregularPluralHuman{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&irregularPluralHuman{}).First(&reloaded))

	carbon.SetTestNow(carbon.Parse("2018-05-01 15:16:17"))

	// $human->irregularPluralTokens()->sync($tokenIds): touchingParent() bumps the parent's
	// updated_at — oracle: `update "irregular_plural_humans" set "updated_at" = ? where "id" = ?`
	// ["2018-05-01 15:16:17", 1].
	_, err := cloneRelationQuery(t, query).Relation(&reloaded, "IrregularPluralTokens").Sync([]any{token.ID})
	require.NoError(t, err)

	refreshed := irregularPluralHuman{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&irregularPluralHuman{}).Where("id", reloaded.ID).First(&refreshed))

	require.NotNil(t, refreshed.CreatedAt)
	require.NotNil(t, refreshed.UpdatedAt)
	assert.Equal(t, "2018-05-01 12:13:14", refreshed.CreatedAt.ToDateTimeString())
	assert.Equal(t, "2018-05-01 15:16:17", refreshed.UpdatedAt.ToDateTimeString())
}

func TestIrregularPlural_ItPluralizesMorphToManyRelationships(t *testing.T) {
	query := newRelationQuery(t)
	irregularPluralSchema(t, query)

	// $human = IrregularPluralHuman::create(['email' => 'bobby@example.com']);
	human := irregularPluralHuman{Email: "bobby@example.com"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&human))

	// $human->mottoes()->create(['name' => 'Real eyes realize real lies']);
	motto := irregularPluralMotto{Name: "Real eyes realize real lies"}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&human, "Mottoes").Create(&motto))

	// $motto = IrregularPluralMotto::query()->first();
	// $this->assertSame('Real eyes realize real lies', $motto->name);
	found := irregularPluralMotto{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&irregularPluralMotto{}).First(&found))
	assert.Equal(t, "Real eyes realize real lies", found.Name)
}
