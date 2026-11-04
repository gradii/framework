package belongs_to_many_with_default_attributes

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
)

// This file ports Laravel's tests/Database/DatabaseEloquentBelongsToManyWithDefaultAttributesTest.php
// (12.x) onto the framework ORM: one Go test per Laravel test method, named
// TestBelongsToManyWithDefaultAttributes_<method without the test prefix>.
//
// The corpus is the belongs-to-many home of withPivotValue(['is_admin' => 1]) over the shape its
// getRelationArguments() mock implies: a parent model keyed 1, a users related table reached
// through a club_user pivot carrying the is_admin column. Both corpus tests build the relation
// against a Mockery-mocked builder (only touchIfTouching is stubbed, so the BelongsToMany
// constructor runs its real addConstraints) and assert Mockery call expectations, never rows. The
// port measures the same behavior against the harness sqlite database:
//
//   - testWithPivotValueMethodSetsWhereConditionsForFetching pins the relation's query conditions:
//     the pivot join plus the wheres "club_user"."club_id" = 1 (added by the relation's
//     constraints) and "club_user"."is_admin" = 1 (added by withPivotValue's wherePivot). The Go
//     ORM has no runtime withPivotValue — pivot scoping is the declaration-time OnPivotQuery
//     callback — so the "Users" declaration carries the same condition as its OnPivotQuery scope:
//     Where("is_admin", 1). The test asserts the compiled query (the where clause byte for byte
//     from the oracle) and then loads the relation, the fetch the conditions govern.
//   - testWithPivotValueMethodSetsDefaultArgumentsForInsertion pins that a later attach(1)
//     inserts [['club_id' => 1, 'user_id' => 1, 'is_admin' => 1]] — withPivotValue's recorded
//     pivot value stamped onto the pivot insert. The relation object records the pair at runtime
//     (WithPivotValue) and the attach shell stamps it onto the pivot row.
//
// # Oracle measurements
//
// illuminate/database v12.55.1 through .claude/laravel-oracle/oracle.php, building the corpus's
// relation (belongsToMany over 'club_user', 'club_id', 'user_id', parent id 1) with
// withPivotValue(['is_admin' => 1]) and adding a Capsule-backed sqlite execution of it:
//
//   - toSql (sqlite): select * from "users" inner join "club_user" on "users"."id" =
//     "club_user"."user_id" where "club_user"."club_id" = ? and "club_user"."is_admin" = ? with
//     bindings [1, 1]; the wheres list is [club_user.club_id = 1, club_user.is_admin = 1], both
//     Basic with the "and" boolean — exactly the corpus's mocked join + two where calls.
//   - toRawSql: ... where "club_user"."club_id" = 1 and "club_user"."is_admin" = 1.
//   - executed against the rows seeded below (club 1: pivot (1, 1, is_admin 1) and (1, 2,
//     is_admin 0); club 2: pivot (2, 3, is_admin 1)), the fetch returns only user 1 ('admin'):
//     the is_admin condition filters user 2 out and the club_id scope excludes club 2's user 3.
//   - attach(1) after withPivotValue(['is_admin' => 1]) writes
//     insert into "club_user" ("club_id", "is_admin", "user_id") values (3, 1, 1) — is_admin = 1
//     rides the pivot insert, the corpus's mocked insert arguments.
//
// # Probes of the Go ORM against the harness database (this package's scratch test, since removed)
//
//   - Related(&club, "Users") compiles select * from "users" inner join "club_user" ON
//     "club_user"."user_id" = "users"."id" where "club_user"."club_id" = ? and
//     "club_user"."is_admin" = ? — the where clause is the oracle's string byte for byte; the join
//     differs only in operand order and the ON keyword's case, the framework's spelling, which
//     this corpus's withPivotValue assertions do not reach.
//   - Load(&club, "Users") returns only the is_admin = 1 row ('admin'), matching the oracle
//     execution.
//   - Related(&club, "Users").WithPivotValue("is_admin", 1).Attach stamps the recorded pair onto
//     the pivot row — the oracle's attach after withPivotValue carries is_admin = 1.

// --- models ------------------------------------------------------------------

// belongsToManyWithDefaultAttributesClub ports the corpus's parent model. The mock exposes only
// getKey() / getAttribute('id') = 1; the Go model carries the id and the Users relation. Users is
// the relation getRelationArguments() builds — the users table reached through the club_user
// pivot — with withPivotValue(['is_admin' => 1])'s wherePivot condition carried as the
// declaration-time OnPivotQuery scope, the Go ORM's home for pivot read conditions.
type belongsToManyWithDefaultAttributesClub struct {
	ID uint `gorm:"column:id;primaryKey;autoIncrement"`

	Users []*belongsToManyWithDefaultAttributesUser `gorm:"-"`
}

func (belongsToManyWithDefaultAttributesClub) TableName() string { return "clubs" }

func (belongsToManyWithDefaultAttributesClub) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		// users(): belongsToMany(User, 'club_user', 'club_id', 'user_id')
		//   ->withPivotValue(['is_admin' => 1]).
		"Users": contractsorm.Many2Many{
			Related:         &belongsToManyWithDefaultAttributesUser{},
			Table:           "club_user",
			ForeignPivotKey: "club_id",
			RelatedPivotKey: "user_id",
			OnPivotQuery: func(query contractsorm.PivotQuery) contractsorm.PivotQuery {
				return query.Where("is_admin", 1)
			},
		},
	}
}

// belongsToManyWithDefaultAttributesUser ports the related model: getTable() = 'users' and
// getKeyName() = 'id'.
type belongsToManyWithDefaultAttributesUser struct {
	ID   uint   `gorm:"column:id;primaryKey;autoIncrement"`
	Name string `gorm:"column:name"`
}

func (belongsToManyWithDefaultAttributesUser) TableName() string { return "users" }

// --- schema and seeding ------------------------------------------------------

// belongsToManyWithDefaultAttributesSchema creates the tables the corpus's mock implies: users as
// the related table and club_user as the pivot with the is_admin column withPivotValue names.
// clubs exists so the parent rows seeded through the ORM are real.
func belongsToManyWithDefaultAttributesSchema(t *testing.T, query contractsorm.Query) {
	t.Helper()

	runRelationSchema(t, query,
		`create table clubs (id integer primary key autoincrement)`,
		`create table users (id integer primary key autoincrement, name varchar not null)`,
		`create table club_user (club_id integer not null, user_id integer not null, is_admin integer null)`,
	)
}

// belongsToManyWithDefaultAttributesSeed ports the rows the corpus implies: two clubs, three
// users, and pivot rows — club 1 holds user 1 with is_admin 1 and user 2 with is_admin 0; club 2
// holds user 3 with is_admin 1, so a fetch that ignored the club_id scope would see a second row.
// The pivot rows go through the relation writer's with-attrs entry, the ORM's per-row pivot
// attribute surface.
func belongsToManyWithDefaultAttributesSeed(t *testing.T, query contractsorm.Query) {
	t.Helper()

	for _, id := range []uint{1, 2} {
		club := belongsToManyWithDefaultAttributesClub{ID: id}
		require.NoError(t, cloneRelationQuery(t, query).Create(&club))
	}

	users := []belongsToManyWithDefaultAttributesUser{
		{ID: 1, Name: "admin"},
		{ID: 2, Name: "plain"},
		{ID: 3, Name: "other-club-admin"},
	}
	for i := range users {
		require.NoError(t, cloneRelationQuery(t, query).Create(&users[i]))
	}

	club1 := belongsToManyWithDefaultAttributesClub{ID: 1}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&club1, "Users").AttachWithPivot(map[any]map[string]any{
		uint(1): {"is_admin": 1},
		uint(2): {"is_admin": 0},
	}))

	club2 := belongsToManyWithDefaultAttributesClub{ID: 2}
	require.NoError(t, cloneRelationQuery(t, query).Relation(&club2, "Users").AttachWithPivot(map[any]map[string]any{
		uint(3): {"is_admin": 1},
	}))
}

// whereClause returns a compiled statement's WHERE clause, from the first " where " to the end —
// the fragment this corpus's fetch-condition assertions observe.
func whereClause(t *testing.T, statement string) string {
	t.Helper()

	index := strings.Index(statement, " where ")
	require.GreaterOrEqual(t, index, 0, statement)

	return statement[index+1:]
}

// --- ports of the corpus -----------------------------------------------------

// TestBelongsToManyWithDefaultAttributes_WithPivotValueMethodSetsWhereConditionsForFetching ports
// testWithPivotValueMethodSetsWhereConditionsForFetching: withPivotValue(['is_admin' => 1]) adds
// the where condition "club_user"."is_admin" = 1 to the relation's query, alongside the pivot join
// and the "club_user"."club_id" = 1 parent scope its constraints add. The corpus verifies those
// three builder calls with Mockery expectations; the port asserts them on the compiled query and
// then fetches through the relation, the behavior the conditions govern.
func TestBelongsToManyWithDefaultAttributes_WithPivotValueMethodSetsWhereConditionsForFetching(t *testing.T) {
	query := newRelationQuery(t)
	belongsToManyWithDefaultAttributesSchema(t, query)
	belongsToManyWithDefaultAttributesSeed(t, query)

	club := belongsToManyWithDefaultAttributesClub{ID: 1}
	dest := &belongsToManyWithDefaultAttributesUser{}

	// Oracle (sqlite): the relation compiles
	//   select * from "users" inner join "club_user" on "users"."id" = "club_user"."user_id"
	//   where "club_user"."club_id" = ? and "club_user"."is_admin" = ?
	// with bindings [1, 1]. Our where clause is that string byte for byte; the join renders with
	// the operands swapped and "ON" uppercased, the framework's join spelling, outside this
	// corpus's assertions.
	statement := cloneRelationQuery(t, query).Related(&club, "Users").ToSql().Get(dest)
	assert.Contains(t, statement, `inner join "club_user"`)
	assert.Equal(t, 1, strings.Count(statement, "inner join"), "the corpus's mocked builder sees exactly one join")
	assert.Equal(t,
		`where "club_user"."club_id" = ? and "club_user"."is_admin" = ?`,
		whereClause(t, statement))
	assert.Equal(t, 2, strings.Count(statement, "?"), "the corpus's mocked builder sees exactly two wheres")

	// The oracle's toRawSql renders the bound values as
	//   ... where "club_user"."club_id" = 1 and "club_user"."is_admin" = 1
	raw := cloneRelationQuery(t, query).Related(&club, "Users").ToRawSql().Get(dest)
	assert.Equal(t,
		`where "club_user"."club_id" = 1 and "club_user"."is_admin" = 1`,
		whereClause(t, raw))

	// The oracle executes this fetch against the same rows and returns only user 1 ('admin') —
	// the is_admin condition filters user 2 out, the club_id scope excludes club 2's user 3.
	// Laravel lazy-loads the relation through property access; the port loads it explicitly.
	loaded := belongsToManyWithDefaultAttributesClub{ID: 1}
	require.NoError(t, cloneRelationQuery(t, query).Load(&loaded, "Users"))
	require.Len(t, loaded.Users, 1)
	assert.Equal(t, uint(1), loaded.Users[0].ID)
	assert.Equal(t, "admin", loaded.Users[0].Name)
}

// TestBelongsToManyWithDefaultAttributes_WithPivotValueMethodSetsDefaultArgumentsForInsertion
// ports testWithPivotValueMethodSetsDefaultArgumentsForInsertion: after
// withPivotValue(['is_admin' => 1]), attach(1) inserts [['club_id' => 1, 'user_id' => 1,
// 'is_admin' => 1]] — the pivot value stamped onto the pivot row. The corpus pins the insert
// arguments through a mocked query; the port probed the same write against the harness database.
func TestBelongsToManyWithDefaultAttributes_WithPivotValueMethodSetsDefaultArgumentsForInsertion(t *testing.T) {
	query := newRelationQuery(t)
	belongsToManyWithDefaultAttributesSchema(t, query)

	// The corpus builds the relation and calls withPivotValue(['is_admin' => 1]) at runtime; the
	// Go port reaches the same relation object through Related and records the pair the same way.
	club := belongsToManyWithDefaultAttributesClub{ID: 1}
	require.NoError(t, cloneRelationQuery(t, query).Create(&club))
	user := belongsToManyWithDefaultAttributesUser{ID: 1, Name: "admin"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&user))

	relation, ok := cloneRelationQuery(t, query).Related(&club, "Users").(contractsorm.BelongsToManyRelation)
	require.True(t, ok, "Related must return a many-to-many relation object")
	require.NoError(t, relation.WithPivotValue("is_admin", 1).Attach([]any{uint(1)}))

	// Oracle (sqlite): the attach after withPivotValue(['is_admin' => 1]) writes
	//   insert into "club_user" ("club_id", "is_admin", "user_id") values (1, 1, 1)
	// (the corpus pins [['club_id' => 1, 'user_id' => 1, 'is_admin' => 1]]).
	rows := []belongsToManyWithDefaultAttributesPivotRow{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&belongsToManyWithDefaultAttributesPivotRow{}).Where("club_id", 1).Get(&rows))
	require.Len(t, rows, 1)
	assert.Equal(t, uint(1), rows[0].ClubID)
	assert.Equal(t, uint(1), rows[0].UserID)
	require.NotNil(t, rows[0].IsAdmin)
	assert.Equal(t, 1, *rows[0].IsAdmin)
}

// belongsToManyWithDefaultAttributesPivotRow reads back a club_user row for the insertion port.
type belongsToManyWithDefaultAttributesPivotRow struct {
	ClubID  uint `gorm:"column:club_id"`
	UserID  uint `gorm:"column:user_id"`
	IsAdmin *int `gorm:"column:is_admin"`
}

func (belongsToManyWithDefaultAttributesPivotRow) TableName() string { return "club_user" }

// TestBelongsToManyWithDefaultAttributes_WithPivotValueStampsSyncAndToggle pins that the
// WithPivotValue-recorded pair threads into the pivot INSERT of the non-OrFail Sync and Toggle
// verbs, not only Attach: the round-7 verify found the sync/toggle threading unpinned (mutants
// dropping m.pivotValues... from the leaf Sync/Toggle left the suite green). Oracle v12.55.1,
// executed: /tmp/r7pv/attach_pv_probe.php A5 (sync) and A6 (toggle) both insert with the recorded
// value.
func TestBelongsToManyWithDefaultAttributes_WithPivotValueStampsSyncAndToggle(t *testing.T) {
	query := newRelationQuery(t)
	belongsToManyWithDefaultAttributesSchema(t, query)

	for _, id := range []uint{1, 2} {
		club := belongsToManyWithDefaultAttributesClub{ID: id}
		require.NoError(t, cloneRelationQuery(t, query).Create(&club))
		user := belongsToManyWithDefaultAttributesUser{ID: id, Name: "u"}
		require.NoError(t, cloneRelationQuery(t, query).Create(&user))
	}

	mustRelation := func(clubID uint) contractsorm.BelongsToManyRelation {
		t.Helper()

		club := belongsToManyWithDefaultAttributesClub{ID: clubID}
		relation, ok := cloneRelationQuery(t, query).Related(&club, "Users").(contractsorm.BelongsToManyRelation)
		require.True(t, ok, "Related must return a many-to-many relation object")

		return relation.WithPivotValue("is_admin", 1)
	}

	_, err := mustRelation(1).Sync([]any{uint(1)})
	require.NoError(t, err)
	_, err = mustRelation(2).Toggle([]any{uint(2)})
	require.NoError(t, err)

	rows := []belongsToManyWithDefaultAttributesPivotRow{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&belongsToManyWithDefaultAttributesPivotRow{}).Get(&rows))
	require.Len(t, rows, 2)
	for _, row := range rows {
		require.NotNil(t, row.IsAdmin, "club %d user %d lost the recorded pivot value", row.ClubID, row.UserID)
		assert.Equal(t, 1, *row.IsAdmin)
	}
}
