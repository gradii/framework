package relations_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsdatabase "github.com/gradii/framework/contracts/database"
	contractsorm "github.com/gradii/framework/contracts/database/orm"
	databasedriver "github.com/gradii/framework/database/driver"
	databasegorm "github.com/gradii/framework/database/gorm"
	querybuilder "github.com/gradii/framework/database/query"
)

// This file closes the coverage the round-7 attach/sync/toggle pins left open (candidate 30): the
// create() / save() / firstOrCreate() write shells must also carry WithPivotValue's recorded pairs
// into the pivot INSERT. Laravel routes every BelongsToMany write through baseAttachRecord
// (InteractsWithPivotTable.php:454-472, the recorded-values loop at :467): BelongsToMany::create =
// newInstance + save + attach ($this->attach($instance, $joining, $touch), BelongsToMany.php:1344
// -> :1417), BelongsToMany::save = $model->save() + attach (:1276), and BelongsToMany::firstOrCreate
// -> createOrFirst -> create (:655). Oracle v12.55.1, executed, /tmp/r9pv/create_pv_probe.php:
//
//	C1  create()  + withPivotValue('is_admin','gold')  -> insert into "club_user" ("club_id","is_admin","user_id") values (1,'gold',1)
//	C4  ... two withPivotValue on one column           -> the last recorded pair wins ('b')
//	C5  save()    + withPivotValue('is_admin','gold')  -> the pivot INSERT carries 'gold'
//	C6  save($m, ['is_admin'=>'caller'])               -> the caller's pivot attribute wins ('caller')
//	C7  firstOrCreate() miss                           -> related insert, pivot INSERT carries 'gold'
//	C8  firstOrCreate() hit-unattached                 -> pivot INSERT carries 'gold'
//	C10 morphToMany create() + withPivotValue('tier','gold') -> insert into "taggables" (...,"tier") values (...,'gold')

// --- a recording transport that can answer LastInsertId -----------------------------------------
//
// The shared newRelationTestBuilder's driver refuses LastInsertId (it returns driver.RowsAffected,
// whose LastInsertId errors) — enough for the attach / sync / toggle shapes, which insert no
// related row, but not for create() / save() / firstOrCreate(): those insert a related row and read
// its generated key back before writing the pivot row, so the shared transport aborts before the
// pivot INSERT. This variant answers LastInsertId with 0 so the create path proceeds and the pivot
// statement is recorded. The recorder is the shared statementRecorder / emptyRows.

type keyedResult struct{}

// LastInsertId answers a fixed generated key so the create path leaves the related row with a
// deterministic key (a real driver returns the row's own key).
func (keyedResult) LastInsertId() (int64, error) { return 42, nil }
func (keyedResult) RowsAffected() (int64, error) { return 0, nil }

type keyedConnector struct{ recorder *statementRecorder }

func (c keyedConnector) Connect(context.Context) (driver.Conn, error) {
	return &keyedConn{recorder: c.recorder}, nil
}
func (c keyedConnector) Driver() driver.Driver { return keyedDriver(c) }

type keyedDriver struct{ recorder *statementRecorder }

func (d keyedDriver) Open(string) (driver.Conn, error) { return &keyedConn{recorder: d.recorder}, nil }

type keyedConn struct{ recorder *statementRecorder }

func (c *keyedConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (c *keyedConn) Close() error                        { return nil }
func (c *keyedConn) Begin() (driver.Tx, error) {
	return nil, errors.New("the keyed recording driver does not support transactions")
}
func (c *keyedConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	c.recorder.record(query, args)

	return keyedResult{}, nil
}
func (c *keyedConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.recorder.record(query, args)

	return emptyRows{}, nil
}
func (c *keyedConn) Ping(context.Context) error { return nil }

// newKeyedRelationTestBuilder is newRelationTestBuilder over the LastInsertId-capable transport.
func newKeyedRelationTestBuilder(t *testing.T) (*databasegorm.OrmBuilder, *statementRecorder) {
	t.Helper()

	recorder := &statementRecorder{}
	db := sql.OpenDB(keyedConnector{recorder: recorder})

	grammar := querybuilder.NewBaseGrammar("")
	components := databasedriver.QueryComponents{Grammar: grammar, Processor: querybuilder.NewProcessor("")}

	builder := databasegorm.NewOrmBuilder(
		context.Background(),
		nil,
		contractsdatabase.Config{Driver: "sqlite"},
		db,
		nil,
		nil,
		nil,
		nil,
		&databasegorm.Conditions{},
		nil,
		components,
	)
	t.Cleanup(func() { _ = db.Close() })

	return builder, recorder
}

// mmPivotInsert returns the single pivot INSERT the recorded statements produced for table.
func mmPivotInsert(t *testing.T, recorder *statementRecorder, table string) recordedStatement {
	t.Helper()

	found := make([]recordedStatement, 0, 1)
	for _, statement := range recorder.take() {
		if strings.HasPrefix(statement.query, `insert into "`+table+`"`) {
			found = append(found, statement)
		}
	}
	require.Len(t, found, 1, "exactly one pivot INSERT for %s", table)

	return found[0]
}

// TestMany2Many_WithPivotValueStampsCreateSaveFirstOrCreate pins create / save / saveWithPivot /
// firstOrCreate (candidate 30): every one routes the recorded pair into the pivot INSERT, with the
// last recorded pair per column winning and the caller's attributes beating the recorded default.
func TestMany2Many_WithPivotValueStampsCreateSaveFirstOrCreate(t *testing.T) {
	cases := []struct {
		name    string
		dup     bool
		run     func(t *testing.T, relation contractsorm.BelongsToManyRelation)
		wantSQL string
		want    []any
	}{
		{
			name: "create",
			run: func(t *testing.T, relation contractsorm.BelongsToManyRelation) {
				require.NoError(t, relation.Create(&mmRole{ID: 5}))
			},
			wantSQL: `insert into "mm_user_roles" ("role", "role_id", "user_id") values (?, ?, ?)`,
			want:    []any{"gold", int64(42), int64(7)},
		},
		{
			name: "save",
			run: func(t *testing.T, relation contractsorm.BelongsToManyRelation) {
				require.NoError(t, relation.Save(&mmRole{ID: 6}))
			},
			wantSQL: `insert into "mm_user_roles" ("role", "role_id", "user_id") values (?, ?, ?)`,
			want:    []any{"gold", int64(6), int64(7)},
		},
		{
			name: "saveWithPivotCallerWins",
			run: func(t *testing.T, relation contractsorm.BelongsToManyRelation) {
				require.NoError(t, relation.SaveWithPivot(&mmRole{ID: 8}, map[string]any{"role": "caller"}))
			},
			wantSQL: `insert into "mm_user_roles" ("role", "role_id", "user_id") values (?, ?, ?)`,
			want:    []any{"caller", int64(8), int64(7)},
		},
		{
			// The caller's attribute sits on a column the recorded pair does not name, so the
			// shell must still stamp the recorded "role" — this is the case that fails when the
			// SaveWithPivot shell's recorded threading is dropped (a caller override on the SAME
			// column, above, yields identical SQL either way and cannot discriminate the shell).
			name: "saveWithPivotRecordedLands",
			run: func(t *testing.T, relation contractsorm.BelongsToManyRelation) {
				require.NoError(t, relation.SaveWithPivot(&mmRole{ID: 9}, map[string]any{"note": "n"}))
			},
			wantSQL: `insert into "mm_user_roles" ("note", "role", "role_id", "user_id") values (?, ?, ?, ?)`,
			want:    []any{"n", "gold", int64(9), int64(7)},
		},
		{
			name: "firstOrCreateMiss",
			run: func(t *testing.T, relation contractsorm.BelongsToManyRelation) {
				require.NoError(t, relation.FirstOrCreate(&mmRole{}, map[string]any{"name": "admin"}))
			},
			wantSQL: `insert into "mm_user_roles" ("role", "role_id", "user_id") values (?, ?, ?)`,
			want:    []any{"gold", int64(42), int64(7)},
		},
		{
			name: "createDuplicateLastWins",
			dup:  true,
			run: func(t *testing.T, relation contractsorm.BelongsToManyRelation) {
				require.NoError(t, relation.Create(&mmRole{ID: 9}))
			},
			wantSQL: `insert into "mm_user_roles" ("role", "role_id", "user_id") values (?, ?, ?)`,
			want:    []any{"b", int64(42), int64(7)},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			builder, recorder := newKeyedRelationTestBuilder(t)

			relation := mmRolesRelation(t, builder, &mmUser{ID: 7}).WithPivotValue("role", "gold")
			if testCase.dup {
				relation = relation.WithPivotValue("role", "b") // a then b: the last recorded pair wins
			}
			testCase.run(t, relation)

			insert := mmPivotInsert(t, recorder, "mm_user_roles")
			assert.Equal(t, testCase.wantSQL, insert.query)
			require.Len(t, insert.args, len(testCase.want))
			for i := range testCase.want {
				assert.Equal(t, testCase.want[i], deref(insert.args[i]))
			}
		})
	}
}

// TestMany2Many_WithPivotValueStampsMorphCreate pins the MorphToMany create() arm: the recorded
// pair rides the morph pivot INSERT beside the morph pair (oracle C10). The mm_post morph value is
// the MorphClass() string mmPost declares.
func TestMany2Many_WithPivotValueStampsMorphCreate(t *testing.T) {
	builder, recorder := newKeyedRelationTestBuilder(t)

	relation := mmTagsRelation(t, builder, &mmPost{ID: 3}).WithPivotValue("tier", "gold")
	require.NoError(t, relation.Create(&mmTag{ID: 4}))

	insert := mmPivotInsert(t, recorder, "mm_taggables")
	assert.Equal(t, `insert into "mm_taggables" ("tag_id", "taggable_id", "taggable_type", "tier") values (?, ?, ?, ?)`, insert.query)
	require.Len(t, insert.args, 4)
	assert.EqualValues(t, 42, deref(insert.args[0]))
	assert.EqualValues(t, 3, deref(insert.args[1]))
	assert.Equal(t, "mm_post", deref(insert.args[2]))
	assert.Equal(t, "gold", deref(insert.args[3]))
}

// TestMany2Many_CreateWithoutPivotValueOmitsTheColumn is the regression guard: create() with nothing
// recorded runs the plain two-column pivot INSERT (oracle C9).
func TestMany2Many_CreateWithoutPivotValueOmitsTheColumn(t *testing.T) {
	builder, recorder := newKeyedRelationTestBuilder(t)

	relation := mmRolesRelation(t, builder, &mmUser{ID: 7})
	require.NoError(t, relation.Create(&mmRole{ID: 3}))

	insert := mmPivotInsert(t, recorder, "mm_user_roles")
	assert.Equal(t, `insert into "mm_user_roles" ("role_id", "user_id") values (?, ?)`, insert.query)
	require.Len(t, insert.args, 2)
	assert.EqualValues(t, 42, deref(insert.args[0]))
	assert.EqualValues(t, 7, deref(insert.args[1]))
}
