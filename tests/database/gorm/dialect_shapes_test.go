package gorm

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"os"
	"strings"
	"testing"

	nativemysql "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsdatabase "github.com/gradii/framework/contracts/database"
	contractsdriver "github.com/gradii/framework/contracts/database/driver"
	contractsorm "github.com/gradii/framework/contracts/database/orm"
	databasedriver "github.com/gradii/framework/database/driver"
	databasegorm "github.com/gradii/framework/database/gorm"
	"github.com/gradii/mysql"
	"github.com/gradii/postgres"
	"github.com/gradii/sqlserver"
)

// This file pins two dialect-sensitive ORM shapes against the real driver grammars.
//
// withExists. Laravel's withAggregate selects `exists(<body>) as <alias>` on every grammar
// (QueriesRelationships.php:925-929, vendor v12.55.1). Oracle (php .claude/laravel-oracle/oracle.php
// /tmp/r15bd/withexists.php) toSql for withExists('posts'):
//
//	mysql   select `we_users`.*, exists(select * from `we_posts` where `we_users`.`id` = `we_posts`.`user_id`) as `posts_exists` from `we_users`
//	pgsql   select "we_users".*, exists(select * from "we_posts" where "we_users"."id" = "we_posts"."user_id") as "posts_exists" from "we_users"
//	sqlsrv  select [we_users].*, exists(select * from [we_posts] where [we_users].[id] = [we_posts].[user_id]) as [posts_exists] from [we_users]
//
// The same probe executed the text on real sqlite, MySQL 9.7 and PostgreSQL 18 (1/0 integers on
// sqlite and MySQL, a native boolean on PostgreSQL). T-SQL has no boolean scalar - EXISTS is a
// predicate only and cannot stand in a select list - so the sqlsrv text does not execute; the port
// keeps the CASE fold to a 0/1 integer on SQL Server alone.
//
// The legacy MySQL group limit. A MySQL server before 8.0.11 numbers rows with session variables
// (MySqlGrammar::compileLegacyGroupLimit), and BelongsToMany::limit (:1510-1514) and
// HasOneOrManyThrough::limit (:779-783) swap the partition column for the aliased pivot column and
// laravel_through_key on that grammar. The branch is reached here by giving the real MySQL grammar a
// 5.7.44 server version. Oracle (/tmp/r15bd/legacy.php, a MySqlConnection whose getServerVersion()
// reports 5.7.44, executed against the real MySQL 9.7 server):
//
//	E1 LgUser::with(['roles' => fn ($q) => $q->limit(2)])->get()  -> [[1,[1,2]],[2,[1,2]]]
//	   select `laravel_table`.*, @laravel_row := if(@laravel_group = `pivot_user_id`, @laravel_row + 1, 1) as `laravel_row`,
//	   @laravel_group := `pivot_user_id` from (select @laravel_row := 0, @laravel_group := 0) as `laravel_vars`,
//	   (select `lg_roles`.*, `lg_role_user`.`user_id` as `pivot_user_id`, `lg_role_user`.`role_id` as `pivot_role_id`
//	   from `lg_roles` inner join `lg_role_user` on `lg_roles`.`id` = `lg_role_user`.`role_id`
//	   where `lg_role_user`.`user_id` in (1, 2) order by `pivot_user_id` asc) as `laravel_table`
//	   having `laravel_row` <= 2 order by `laravel_row`
//	E2 LgCountry::with(['posts' => fn ($q) => $q->limit(2)])->get() -> [[1,["p1","p2"]],[2,["q1","q2"]]]
//	   the same envelope over `laravel_through_key`, around (select `lg_tposts`.*, `lg_tusers`.`country_id` as
//	   `laravel_through_key` from `lg_tposts` inner join `lg_tusers` on `lg_tusers`.`id` = `lg_tposts`.`user_id`
//	   where `lg_tusers`.`country_id` in (1, 2) order by `laravel_through_key` asc)
//
// The envelope, the partition column and the inner select list byte-match. Two differences that are
// not this branch's, and are pinned elsewhere, stay out of the assertions: the port's many-to-many
// join renders `ON <pivot>.<related key> = <related>.<key>` (database/gorm/relations/many2many_test.go
// mmPlainReadBase), and the eager key list is bound where Laravel's whereIntegerInRaw inlines it.

type existsUser struct {
	ID    uint          `gorm:"column:id;primaryKey"`
	Posts []*existsPost `gorm:"-"`
}

func (existsUser) TableName() string { return "we_users" }

func (existsUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"posts": contractsorm.HasMany{Related: &existsPost{}, ForeignKey: "user_id"},
	}
}

type existsPost struct {
	ID     uint `gorm:"column:id;primaryKey"`
	UserID uint `gorm:"column:user_id"`
}

func (existsPost) TableName() string { return "we_posts" }

func TestWithExistsFollowsTheDriverGrammar(t *testing.T) {
	cases := []struct {
		name    string
		dialect contractsdriver.Dialect
		want    string
	}{
		{mysql.Name, mysql.NewDialect(),
			"select `we_users`.*, exists(select * from `we_posts` where `we_users`.`id` = `we_posts`.`user_id`) as `posts_exists` from `we_users`"},
		{postgres.Name, postgres.NewDialect(),
			`select "we_users".*, exists(select * from "we_posts" where "we_users"."id" = "we_posts"."user_id") as "posts_exists" from "we_users"`},
		// The oracle's sqlsrv text (`exists(...) as [posts_exists]`) is not valid T-SQL; the body and
		// the bracket quoting are Laravel's, the CASE fold is the port's.
		{sqlserver.Name, sqlserver.NewDialect(),
			"select [we_users].*, CASE WHEN EXISTS (select * from [we_posts] where [we_users].[id] = [we_posts].[user_id]) THEN 1 ELSE 0 END AS [posts_exists] from [we_users]"},
	}

	for _, c := range cases {
		query := newDialectQuery(t, c.dialect, c.name, nil, &existsUser{})
		assert.Equal(t, c.want, query.WithExists("posts").ToSql().Get(&[]existsUser{}), c.name)
	}
}

// --- the legacy MySQL group limit --------------------------------------------------------------

type lgUser struct {
	ID    uint      `gorm:"column:id;primaryKey"`
	Roles []*lgRole `gorm:"-"`
}

func (lgUser) TableName() string { return "lg_users" }

func (lgUser) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Roles": contractsorm.Many2Many{Related: &lgRole{}, Table: "lg_role_user", ForeignPivotKey: "user_id", RelatedPivotKey: "role_id"},
	}
}

type lgRole struct {
	ID   uint   `gorm:"column:id;primaryKey"`
	Name string `gorm:"column:name"`
}

func (lgRole) TableName() string { return "lg_roles" }

type lgCountry struct {
	ID    uint      `gorm:"column:id;primaryKey"`
	Posts []*lgPost `gorm:"-"`
}

func (lgCountry) TableName() string { return "lg_countries" }

func (lgCountry) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Posts": contractsorm.HasManyThrough{Related: &lgPost{}, Through: &lgTUser{}, FirstKey: "country_id", SecondKey: "user_id"},
	}
}

type lgTUser struct {
	ID        uint `gorm:"column:id;primaryKey"`
	CountryID uint `gorm:"column:country_id"`
}

func (lgTUser) TableName() string { return "lg_tusers" }

type lgPost struct {
	ID     uint   `gorm:"column:id;primaryKey"`
	UserID uint   `gorm:"column:user_id"`
	Title  string `gorm:"column:title"`
}

func (lgPost) TableName() string { return "lg_tposts" }

// mysqlQueryOnVersion builds an ORM query on the real MySQL grammar reporting the given server
// version, over pool.
func mysqlQueryOnVersion(t *testing.T, version string, pool *sql.DB) *databasegorm.OrmBuilder {
	t.Helper()

	options := contractsdriver.GrammarOptions{Driver: mysql.Name, ServerVersion: func() (string, error) { return version, nil }}
	components, err := databasedriver.ResolveQueryComponents(mysql.NewDialect(), options)
	require.NoError(t, err)

	conditions := databasegorm.Conditions{}

	return databasegorm.NewOrmBuilder(context.Background(), nil, contractsdatabase.Config{Driver: mysql.Name},
		pool, nil, nil, nil, nil, &conditions, nil, components)
}

const (
	legacyRolesHead = "select `laravel_table`.*, @laravel_row := if(@laravel_group = `pivot_user_id`, @laravel_row + 1, 1) as `laravel_row`, @laravel_group := `pivot_user_id` from (select @laravel_row := 0, @laravel_group := 0) as `laravel_vars`, (select `lg_roles`.*, `lg_role_user`.`user_id` as `pivot_user_id`, `lg_role_user`.`role_id` as `pivot_role_id` from `lg_roles` inner join `lg_role_user` "
	legacyRolesTail = " order by `pivot_user_id` asc) as `laravel_table` having `laravel_row` <= 2 order by `laravel_row`"
	legacyPostsHead = "select `laravel_table`.*, @laravel_row := if(@laravel_group = `laravel_through_key`, @laravel_row + 1, 1) as `laravel_row`, @laravel_group := `laravel_through_key` from (select @laravel_row := 0, @laravel_group := 0) as `laravel_vars`, (select `lg_tposts`.*, `lg_tusers`.`country_id` as `laravel_through_key` from `lg_tposts` inner join `lg_tusers` on `lg_tusers`.`id` = `lg_tposts`.`user_id` where `lg_tusers`.`country_id` in "
	legacyPostsTail = " order by `laravel_through_key` asc) as `laravel_table` having `laravel_row` <= 2 order by `laravel_row`"
)

// statementContaining returns the one recorded statement that contains fragment.
func statementContaining(t *testing.T, recorder *dialectRecorder, fragment string) string {
	t.Helper()

	recorder.mu.Lock()
	defer recorder.mu.Unlock()

	found := []string{}
	for _, statement := range recorder.stmts {
		if strings.Contains(statement.sql, fragment) {
			found = append(found, statement.sql)
		}
	}
	require.Len(t, found, 1, "statements containing %q", fragment)

	return found[0]
}

func TestEagerLimitOnLegacyMySQLPartitionsByTheAliasedColumn(t *testing.T) {
	recorder := &dialectRecorder{}
	pool := sql.OpenDB(dialectConnector{recorder: recorder})
	t.Cleanup(func() { _ = pool.Close() })
	recorder.returnRows("from `lg_users`", []string{"id"}, []driver.Value{int64(1)}, []driver.Value{int64(2)})
	recorder.returnRows("from `lg_countries`", []string{"id"}, []driver.Value{int64(1)}, []driver.Value{int64(2)})
	recorder.returnRows("from `lg_tusers`", []string{"country_id", "id"}, []driver.Value{int64(1), int64(1)}, []driver.Value{int64(2), int64(2)})
	limitTwo := func(q contractsorm.Query) contractsorm.Query { return q.Limit(2) }

	// Each read starts from its own copy, the way Orm.Query() hands one out: a read leaves the
	// query's With entries in place, so a shared query would carry Roles into the country read.
	query := mysqlQueryOnVersion(t, "5.7.44", pool)
	require.NoError(t, query.Clone().Model(&lgUser{}).OrderBy("id").With("Roles", limitTwo).Get(&[]lgUser{}))
	require.NoError(t, query.Clone().Model(&lgCountry{}).OrderBy("id").With("Posts", limitTwo).Get(&[]lgCountry{}))

	// Oracle E1: the pivot read partitions by the aliased pivot column, pivot_user_id.
	roles := statementContaining(t, recorder, "from `lg_roles`")
	assert.True(t, strings.HasPrefix(roles, legacyRolesHead), roles)
	assert.True(t, strings.HasSuffix(roles, legacyRolesTail), roles)
	// Oracle E2: the through read partitions by laravel_through_key.
	posts := statementContaining(t, recorder, "from `lg_tposts`")
	assert.True(t, strings.HasPrefix(posts, legacyPostsHead), posts)
	assert.True(t, strings.HasSuffix(posts, legacyPostsTail), posts)

	// 8.0.11 is the first server the modern row_number() rewrite runs on, partitioned by the
	// qualified keys.
	modern := &dialectRecorder{}
	modernPool := sql.OpenDB(dialectConnector{recorder: modern})
	t.Cleanup(func() { _ = modernPool.Close() })
	modern.returnRows("from `lg_users`", []string{"id"}, []driver.Value{int64(1)}, []driver.Value{int64(2)})
	require.NoError(t, mysqlQueryOnVersion(t, "8.0.11", modernPool).Model(&lgUser{}).With("Roles", limitTwo).Get(&[]lgUser{}))
	assert.Contains(t, statementContaining(t, modern, "from `lg_roles`"), "row_number() over (partition by `lg_role_user`.`user_id`) as `laravel_row`")
}

// TestEagerLimitOnLegacyMySQLRunsOnARealServer executes the legacy rewrite on the MySQL server the
// GRADII_TEST_MYSQL_DSN names - the session-variable numbering still runs on MySQL 9 - and reads
// the rows the oracle's E1 / E2 read.
func TestEagerLimitOnLegacyMySQLRunsOnARealServer(t *testing.T) {
	dsn := os.Getenv("GRADII_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("GRADII_TEST_MYSQL_DSN is not set")
	}
	config, err := nativemysql.ParseDSN(dsn)
	require.NoError(t, err)
	config.MultiStatements = true
	connector, err := nativemysql.NewConnector(config)
	require.NoError(t, err)
	pool := sql.OpenDB(connector)
	t.Cleanup(func() { _ = pool.Close() })

	const drop = "drop table if exists lg_role_user, lg_roles, lg_users, lg_tposts, lg_tusers, lg_countries"
	_, err = pool.Exec(drop + `;
		create table lg_users (id int unsigned primary key);
		create table lg_roles (id int unsigned primary key, name varchar(255) not null);
		create table lg_role_user (user_id int unsigned not null, role_id int unsigned not null);
		create table lg_countries (id int unsigned primary key);
		create table lg_tusers (id int unsigned primary key, country_id int unsigned not null);
		create table lg_tposts (id int unsigned primary key auto_increment, user_id int unsigned not null, title varchar(255) not null);
		insert into lg_users values (1), (2);
		insert into lg_roles values (1, 'a'), (2, 'b'), (3, 'c');
		insert into lg_role_user values (1, 1), (1, 2), (1, 3), (2, 1), (2, 2), (2, 3);
		insert into lg_countries values (1), (2);
		insert into lg_tusers values (1, 1), (2, 2);
		insert into lg_tposts (user_id, title) values (1, 'p1'), (1, 'p2'), (1, 'p3'), (2, 'q1'), (2, 'q2')`)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = pool.Exec(drop) })
	limitTwo := func(q contractsorm.Query) contractsorm.Query { return q.Limit(2) }
	query := mysqlQueryOnVersion(t, "5.7.44", pool)

	var users []lgUser
	require.NoError(t, query.Clone().Model(&lgUser{}).OrderBy("id").With("Roles", limitTwo).Get(&users))
	require.Len(t, users, 2)
	for _, user := range users {
		ids := []uint{}
		for _, role := range user.Roles {
			ids = append(ids, role.ID)
		}
		assert.Equal(t, []uint{1, 2}, ids, "user %d", user.ID)
	}

	var countries []lgCountry
	require.NoError(t, query.Clone().Model(&lgCountry{}).OrderBy("id").With("Posts", limitTwo).Get(&countries))
	require.Len(t, countries, 2)
	want := map[uint][]string{1: {"p1", "p2"}, 2: {"q1", "q2"}}
	for _, country := range countries {
		titles := []string{}
		for _, post := range country.Posts {
			titles = append(titles, post.Title)
		}
		assert.Equal(t, want[country.ID], titles, "country %d", country.ID)
	}
}
