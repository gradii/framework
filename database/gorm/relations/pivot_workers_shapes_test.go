package relations_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	contractsdatabase "github.com/gradii/framework/contracts/database"
	contractsorm "github.com/gradii/framework/contracts/database/orm"
	databasedriver "github.com/gradii/framework/database/driver"
	databasegorm "github.com/gradii/framework/database/gorm"
	querybuilder "github.com/gradii/framework/database/query"
)

// The S2 step-3 port (pivot_workers.go) moved the many-to-many / pivot write workers out of
// database/gorm/relation_writes.go. This file pins the statements the pivot shells compile — the
// shells are the one surface that exists unchanged on both sides of the move, so the same
// recording run was taken against the pre-port tree and against the ported tree, and the
// rendered log below is byte-identical on both (the port's own before/after evidence, next to
// the oracle-pinned corpora under tests/database/orm_relation).

// --- fixtures ----------------------------------------------------------------------------------

// pwParent declares the three many-to-many shapes the pivot workers compile statements for: a
// plain Many2Many, a MorphToMany (the morph-type predicate and column) and a Many2Many with an
// OnPivotQuery scope (replayed into the detach / update / existence reads, never into the insert).
type pwParent struct {
	ID     uint     `gorm:"column:id;primaryKey"`
	Kids   []*pwKid `gorm:"-"`
	Tags   []*pwTag `gorm:"-"`
	Scoped []*pwKid `gorm:"-"`
}

func (pwParent) TableName() string { return "pw_parents" }

func (pwParent) Relations() map[string]contractsorm.Relation {
	return map[string]contractsorm.Relation{
		"Kids": contractsorm.Many2Many{Related: &pwKid{}, Table: "pw_parent_kids", ForeignPivotKey: "parent_id", RelatedPivotKey: "kid_id"},
		"Tags": contractsorm.MorphToMany{Related: &pwTag{}, Name: "taggable", Table: "pw_taggables", ForeignPivotKey: "taggable_id", RelatedPivotKey: "tag_id"},
		"Scoped": contractsorm.Many2Many{
			Related: &pwKid{}, Table: "pw_parent_kids", ForeignPivotKey: "parent_id", RelatedPivotKey: "kid_id",
			OnPivotQuery: func(q contractsorm.PivotQuery) contractsorm.PivotQuery { return q.Where("role", "admin") },
		},
	}
}

type pwKid struct {
	ID uint `gorm:"column:id;primaryKey"`
}

func (pwKid) TableName() string { return "pw_kids" }

type pwTag struct {
	ID uint `gorm:"column:id;primaryKey"`
}

func (pwTag) TableName() string { return "pw_tags" }

// recordPivotShapes drives every pivot shell of the Builder write-shell set through the recording
// transport and renders what it compiled: one block per call — the label, the result and the
// error, then each statement with its bindings in order. Every read answers no rows and every
// write reports zero affected rows, so a sync attaches everything it is handed and detaches
// nothing, and no parent touch runs. Single ids keep the attach order deterministic (the sync
// cores iterate a map).
func recordPivotShapes(t *testing.T) string {
	t.Helper()

	var out strings.Builder
	run := func(label string, fn func(q *databasegorm.OrmBuilder) (any, error)) {
		builder, recorder := newRelationTestBuilder(t)
		result, err := fn(builder)
		fmt.Fprintf(&out, "## %s result=%v err=%v\n", label, result, err)
		for _, statement := range recorder.take() {
			args := make([]any, 0, len(statement.args))
			for _, arg := range statement.args {
				args = append(args, deref(arg))
			}
			fmt.Fprintf(&out, "%s | %v\n", statement.query, args)
		}
	}
	parent := func() *pwParent { return &pwParent{ID: 7} }
	one := []any{uint(5)}
	two := []any{uint(5), uint(6)}
	role := map[string]any{"role": "x"}
	withRole := map[any]map[string]any{uint(5): role}

	run("attach.kids", func(q *databasegorm.OrmBuilder) (any, error) { return nil, q.AttachRelation(parent(), "Kids", one) })
	run("attach.tags", func(q *databasegorm.OrmBuilder) (any, error) { return nil, q.AttachRelation(parent(), "Tags", one) })
	run("attach.scoped", func(q *databasegorm.OrmBuilder) (any, error) { return nil, q.AttachRelation(parent(), "Scoped", one) })
	run("attach.empty", func(q *databasegorm.OrmBuilder) (any, error) { return nil, q.AttachRelation(parent(), "Kids", nil) })
	run("attachWithPivot.kids", func(q *databasegorm.OrmBuilder) (any, error) {
		return nil, q.AttachWithPivotRelation(parent(), "Kids", withRole)
	})
	run("detach.nil", func(q *databasegorm.OrmBuilder) (any, error) { return q.DetachRelation(parent(), "Kids", nil) })
	run("detach.ids", func(q *databasegorm.OrmBuilder) (any, error) { return q.DetachRelation(parent(), "Kids", two) })
	run("detach.tags.ids", func(q *databasegorm.OrmBuilder) (any, error) { return q.DetachRelation(parent(), "Tags", one) })
	run("detach.scoped.nil", func(q *databasegorm.OrmBuilder) (any, error) { return q.DetachRelation(parent(), "Scoped", nil) })
	run("detach.empty", func(q *databasegorm.OrmBuilder) (any, error) { return q.DetachRelation(parent(), "Kids", []any{}) })
	run("detach.nestedEmpty", func(q *databasegorm.OrmBuilder) (any, error) {
		return q.DetachRelation(parent(), "Kids", []any{[]any{}})
	})
	run("sync.one", func(q *databasegorm.OrmBuilder) (any, error) { return q.SyncRelation(parent(), "Kids", one) })
	run("sync.tags.one", func(q *databasegorm.OrmBuilder) (any, error) { return q.SyncRelation(parent(), "Tags", one) })
	run("sync.scoped.one", func(q *databasegorm.OrmBuilder) (any, error) { return q.SyncRelation(parent(), "Scoped", one) })
	run("syncWithoutDetaching.one", func(q *databasegorm.OrmBuilder) (any, error) {
		return q.SyncWithoutDetachingRelation(parent(), "Kids", one)
	})
	run("toggle.one", func(q *databasegorm.OrmBuilder) (any, error) { return q.ToggleRelation(parent(), "Kids", one) })
	run("syncWithPivot.one", func(q *databasegorm.OrmBuilder) (any, error) {
		return q.SyncRelationWithPivot(parent(), "Kids", withRole)
	})
	run("syncWithPivotValues.one", func(q *databasegorm.OrmBuilder) (any, error) {
		return q.SyncRelationWithPivotValues(parent(), "Kids", one, role)
	})
	run("syncWithoutDetachingWithPivot.one", func(q *databasegorm.OrmBuilder) (any, error) {
		return q.SyncWithoutDetachingRelationWithPivot(parent(), "Kids", withRole)
	})
	run("toggleWithPivot.one", func(q *databasegorm.OrmBuilder) (any, error) {
		return q.ToggleRelationWithPivot(parent(), "Kids", withRole)
	})
	run("updateExistingPivot.kids", func(q *databasegorm.OrmBuilder) (any, error) {
		return q.UpdateExistingPivotRelation(parent(), "Kids", uint(5), role)
	})
	run("updateExistingPivot.tags", func(q *databasegorm.OrmBuilder) (any, error) {
		return q.UpdateExistingPivotRelation(parent(), "Tags", uint(5), role)
	})
	run("updateExistingPivot.scoped", func(q *databasegorm.OrmBuilder) (any, error) {
		return q.UpdateExistingPivotRelation(parent(), "Scoped", uint(5), role)
	})
	run("updateExistingPivot.empty", func(q *databasegorm.OrmBuilder) (any, error) {
		return q.UpdateExistingPivotRelation(parent(), "Kids", uint(5), map[string]any{})
	})
	run("guard.valueParent", func(q *databasegorm.OrmBuilder) (any, error) {
		return nil, q.AttachRelation(pwParent{ID: 7}, "Kids", one)
	})
	run("guard.unknownRelation", func(q *databasegorm.OrmBuilder) (any, error) { return nil, q.AttachRelation(parent(), "Nope", one) })
	run("guard.kind", func(q *databasegorm.OrmBuilder) (any, error) { return q.SyncRelation(&sqlUser{ID: 1}, "Books", one) })

	return out.String()
}

func TestPivotWorkers_ShellStatementShapes_UnchangedByThePort(t *testing.T) {
	assert.Equal(t, pivotShellShapes, recordPivotShapes(t))
}

// pivotShellShapes is the recording the pre-port tree (HEAD 6a808447, the gorm-side workers)
// and the ported tree both render for recordPivotShapes.
const pivotShellShapes = `## attach.kids result=<nil> err=<nil>
select "kid_id" from "pw_parent_kids" where "pw_parent_kids"."parent_id" = ? and "pw_parent_kids"."kid_id" in (?) | [7 5]
insert into "pw_parent_kids" ("kid_id", "parent_id") values (?, ?) | [5 7]
## attach.tags result=<nil> err=<nil>
select "tag_id" from "pw_taggables" where "pw_taggables"."taggable_id" = ? and "pw_taggables"."tag_id" in (?) and "pw_taggables"."taggable_type" = ? | [7 5 pw_parents]
insert into "pw_taggables" ("tag_id", "taggable_id", "taggable_type") values (?, ?, ?) | [5 7 pw_parents]
## attach.scoped result=<nil> err=<nil>
select "kid_id" from "pw_parent_kids" where "pw_parent_kids"."role" = ? and "pw_parent_kids"."parent_id" = ? and "pw_parent_kids"."kid_id" in (?) | [admin 7 5]
insert into "pw_parent_kids" ("kid_id", "parent_id") values (?, ?) | [5 7]
## attach.empty result=<nil> err=<nil>
## attachWithPivot.kids result=<nil> err=<nil>
select "kid_id" from "pw_parent_kids" where "pw_parent_kids"."parent_id" = ? and "pw_parent_kids"."kid_id" in (?) | [7 5]
insert into "pw_parent_kids" ("kid_id", "parent_id", "role") values (?, ?, ?) | [5 7 x]
## detach.nil result=0 err=<nil>
delete from "pw_parent_kids" where "pw_parent_kids"."parent_id" = ? | [7]
## detach.ids result=0 err=<nil>
delete from "pw_parent_kids" where "pw_parent_kids"."parent_id" = ? and "pw_parent_kids"."kid_id" in (?, ?) | [7 5 6]
## detach.tags.ids result=0 err=<nil>
delete from "pw_taggables" where "pw_taggables"."taggable_id" = ? and "pw_taggables"."taggable_type" = ? and "pw_taggables"."tag_id" in (?) | [7 pw_parents 5]
## detach.scoped.nil result=0 err=<nil>
delete from "pw_parent_kids" where "pw_parent_kids"."role" = ? and "pw_parent_kids"."parent_id" = ? | [admin 7]
## detach.empty result=0 err=<nil>
## detach.nestedEmpty result=0 err=<nil>
## sync.one result=&{[5] [] []} err=<nil>
select "kid_id" from "pw_parent_kids" where "pw_parent_kids"."parent_id" = ? | [7]
select "kid_id" from "pw_parent_kids" where "pw_parent_kids"."parent_id" = ? and "pw_parent_kids"."kid_id" in (?) | [7 5]
insert into "pw_parent_kids" ("kid_id", "parent_id") values (?, ?) | [5 7]
## sync.tags.one result=&{[5] [] []} err=<nil>
select "tag_id" from "pw_taggables" where "pw_taggables"."taggable_id" = ? and "pw_taggables"."taggable_type" = ? | [7 pw_parents]
select "tag_id" from "pw_taggables" where "pw_taggables"."taggable_id" = ? and "pw_taggables"."tag_id" in (?) and "pw_taggables"."taggable_type" = ? | [7 5 pw_parents]
insert into "pw_taggables" ("tag_id", "taggable_id", "taggable_type") values (?, ?, ?) | [5 7 pw_parents]
## sync.scoped.one result=&{[5] [] []} err=<nil>
select "kid_id" from "pw_parent_kids" where "pw_parent_kids"."role" = ? and "pw_parent_kids"."parent_id" = ? | [admin 7]
select "kid_id" from "pw_parent_kids" where "pw_parent_kids"."role" = ? and "pw_parent_kids"."parent_id" = ? and "pw_parent_kids"."kid_id" in (?) | [admin 7 5]
insert into "pw_parent_kids" ("kid_id", "parent_id") values (?, ?) | [5 7]
## syncWithoutDetaching.one result=&{[5] [] []} err=<nil>
select "kid_id" from "pw_parent_kids" where "pw_parent_kids"."parent_id" = ? | [7]
select "kid_id" from "pw_parent_kids" where "pw_parent_kids"."parent_id" = ? and "pw_parent_kids"."kid_id" in (?) | [7 5]
insert into "pw_parent_kids" ("kid_id", "parent_id") values (?, ?) | [5 7]
## toggle.one result=&{[5] [] []} err=<nil>
select "kid_id" from "pw_parent_kids" where "pw_parent_kids"."parent_id" = ? | [7]
select "kid_id" from "pw_parent_kids" where "pw_parent_kids"."parent_id" = ? and "pw_parent_kids"."kid_id" in (?) | [7 5]
insert into "pw_parent_kids" ("kid_id", "parent_id") values (?, ?) | [5 7]
## syncWithPivot.one result=&{[5] [] []} err=<nil>
select "kid_id" from "pw_parent_kids" where "pw_parent_kids"."parent_id" = ? | [7]
select "kid_id" from "pw_parent_kids" where "pw_parent_kids"."parent_id" = ? and "pw_parent_kids"."kid_id" in (?) | [7 5]
insert into "pw_parent_kids" ("kid_id", "parent_id", "role") values (?, ?, ?) | [5 7 x]
## syncWithPivotValues.one result=&{[5] [] []} err=<nil>
select "kid_id" from "pw_parent_kids" where "pw_parent_kids"."parent_id" = ? | [7]
select "kid_id" from "pw_parent_kids" where "pw_parent_kids"."parent_id" = ? and "pw_parent_kids"."kid_id" in (?) | [7 5]
insert into "pw_parent_kids" ("kid_id", "parent_id", "role") values (?, ?, ?) | [5 7 x]
## syncWithoutDetachingWithPivot.one result=&{[5] [] []} err=<nil>
select "kid_id" from "pw_parent_kids" where "pw_parent_kids"."parent_id" = ? | [7]
select "kid_id" from "pw_parent_kids" where "pw_parent_kids"."parent_id" = ? and "pw_parent_kids"."kid_id" in (?) | [7 5]
insert into "pw_parent_kids" ("kid_id", "parent_id", "role") values (?, ?, ?) | [5 7 x]
## toggleWithPivot.one result=&{[5] [] []} err=<nil>
select "kid_id" from "pw_parent_kids" where "pw_parent_kids"."parent_id" = ? | [7]
select "kid_id" from "pw_parent_kids" where "pw_parent_kids"."parent_id" = ? and "pw_parent_kids"."kid_id" in (?) | [7 5]
insert into "pw_parent_kids" ("kid_id", "parent_id", "role") values (?, ?, ?) | [5 7 x]
## updateExistingPivot.kids result=0 err=<nil>
update "pw_parent_kids" set "role" = ? where "pw_parent_kids"."parent_id" = ? and "pw_parent_kids"."kid_id" = ? | [x 7 5]
## updateExistingPivot.tags result=0 err=<nil>
update "pw_taggables" set "role" = ? where "pw_taggables"."taggable_id" = ? and "pw_taggables"."tag_id" = ? and "pw_taggables"."taggable_type" = ? | [x 7 5 pw_parents]
## updateExistingPivot.scoped result=0 err=<nil>
update "pw_parent_kids" set "role" = ? where "pw_parent_kids"."role" = ? and "pw_parent_kids"."parent_id" = ? and "pw_parent_kids"."kid_id" = ? | [x admin 7 5]
## updateExistingPivot.empty result=0 err=<nil>
## guard.valueParent result=<nil> err=Related: parent must be a non-nil pointer to a struct, got relations_test.pwParent
## guard.unknownRelation result=<nil> err=relation "Nope" not found on model *relations_test.pwParent
## guard.kind result=<nil> err=operation "Sync" is not supported on relation "Books" (kind "hasMany")
`

// --- the statement-order pin ---------------------------------------------------------------------

// The recording above never detaches: every read is empty, so a sync has nothing to remove and the
// attach-versus-detach order of the sync cores is invisible in it. The transport below answers the
// parent-only pivot read (AllPivotIDs: one binding, the parent key) with one row — kid 6 — while
// the id-list reads (two bindings) stay empty, so a sync / toggle of kid 5 both detaches 6 and
// attaches 5, and the rendered log shows which statement runs first. Laravel runs the DELETE
// before the INSERT (InteractsWithPivotTable::sync :123 detach then :133 attachNew; ::toggle :39
// detach then :50 attach; oracle v12.55.1, executed, /tmp/lalign_synccore_order_probe.php: every
// sync / syncWithoutDetaching / toggle / syncWithPivotValues log reads `select`, `delete`,
// then `update` / `insert`).

type orderConnector struct{ recorder *statementRecorder }

func (c orderConnector) Connect(context.Context) (driver.Conn, error) {
	return &orderConn{recorder: c.recorder}, nil
}

func (c orderConnector) Driver() driver.Driver { return orderDriver(c) }

type orderDriver struct{ recorder *statementRecorder }

func (d orderDriver) Open(string) (driver.Conn, error) {
	return &orderConn{recorder: d.recorder}, nil
}

type orderConn struct{ recorder *statementRecorder }

func (c *orderConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }

func (c *orderConn) Close() error { return nil }

func (c *orderConn) Begin() (driver.Tx, error) {
	return nil, errors.New("the order-recording driver does not support transactions")
}

func (c *orderConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	c.recorder.record(query, args)

	return driver.RowsAffected(1), nil
}

func (c *orderConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.recorder.record(query, args)
	if len(args) == 1 {
		return &oneKidRows{}, nil
	}

	return emptyRows{}, nil
}

func (c *orderConn) Ping(context.Context) error { return nil }

// oneKidRows answers the parent-only pivot read with a single attached kid, id 6.
type oneKidRows struct{ done bool }

func (*oneKidRows) Columns() []string { return []string{"kid_id"} }

func (*oneKidRows) Close() error { return nil }

func (r *oneKidRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	dest[0] = int64(6)

	return nil
}

// newOrderTestBuilder is newRelationTestBuilder on the one-row transport.
func newOrderTestBuilder(t *testing.T) (*databasegorm.OrmBuilder, *statementRecorder) {
	t.Helper()

	recorder := &statementRecorder{}
	db := sql.OpenDB(orderConnector{recorder: recorder})

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

// recordPivotOrder drives the sync cores against a parent that already has kid 6 attached and
// renders the statements in execution order.
func recordPivotOrder(t *testing.T) string {
	t.Helper()

	var out strings.Builder
	run := func(label string, fn func(q *databasegorm.OrmBuilder) (any, error)) {
		builder, recorder := newOrderTestBuilder(t)
		result, err := fn(builder)
		fmt.Fprintf(&out, "## %s result=%v err=%v\n", label, result, err)
		for _, statement := range recorder.take() {
			args := make([]any, 0, len(statement.args))
			for _, arg := range statement.args {
				args = append(args, deref(arg))
			}
			fmt.Fprintf(&out, "%s | %v\n", statement.query, args)
		}
	}
	parent := func() *pwParent { return &pwParent{ID: 7} }
	five := []any{uint(5)}
	sixAndFive := []any{uint(6), uint(5)}
	role := map[string]any{"role": "x"}
	fiveWithRole := map[any]map[string]any{uint(5): role}
	sixAndFiveWithRole := map[any]map[string]any{uint(6): role, uint(5): role}

	run("sync.five", func(q *databasegorm.OrmBuilder) (any, error) { return q.SyncRelation(parent(), "Kids", five) })
	run("syncWithoutDetaching.five", func(q *databasegorm.OrmBuilder) (any, error) {
		return q.SyncWithoutDetachingRelation(parent(), "Kids", five)
	})
	run("toggle.sixAndFive", func(q *databasegorm.OrmBuilder) (any, error) {
		return q.ToggleRelation(parent(), "Kids", sixAndFive)
	})
	run("syncWithPivot.five", func(q *databasegorm.OrmBuilder) (any, error) {
		return q.SyncRelationWithPivot(parent(), "Kids", fiveWithRole)
	})
	run("syncWithPivotValues.five", func(q *databasegorm.OrmBuilder) (any, error) {
		return q.SyncRelationWithPivotValues(parent(), "Kids", five, role)
	})
	run("syncWithoutDetachingWithPivot.five", func(q *databasegorm.OrmBuilder) (any, error) {
		return q.SyncWithoutDetachingRelationWithPivot(parent(), "Kids", fiveWithRole)
	})
	run("toggleWithPivot.sixAndFive", func(q *databasegorm.OrmBuilder) (any, error) {
		return q.ToggleRelationWithPivot(parent(), "Kids", sixAndFiveWithRole)
	})

	return out.String()
}

func TestPivotWorkers_SyncCoresDetachBeforeTheyAttach(t *testing.T) {
	assert.Equal(t, pivotOrderShapes, recordPivotOrder(t))
}

// pivotOrderShapes is the Laravel order: the DELETE of the id to detach runs before the INSERT of
// the id to attach, in every core and branch (the syncWithoutDetaching rows have nothing to detach
// and are listed for completeness).
const pivotOrderShapes = `## sync.five result=&{[5] [6] []} err=<nil>
select "kid_id" from "pw_parent_kids" where "pw_parent_kids"."parent_id" = ? | [7]
delete from "pw_parent_kids" where "pw_parent_kids"."parent_id" = ? and "pw_parent_kids"."kid_id" in (?) | [7 6]
select "kid_id" from "pw_parent_kids" where "pw_parent_kids"."parent_id" = ? and "pw_parent_kids"."kid_id" in (?) | [7 5]
insert into "pw_parent_kids" ("kid_id", "parent_id") values (?, ?) | [5 7]
## syncWithoutDetaching.five result=&{[5] [] []} err=<nil>
select "kid_id" from "pw_parent_kids" where "pw_parent_kids"."parent_id" = ? | [7]
select "kid_id" from "pw_parent_kids" where "pw_parent_kids"."parent_id" = ? and "pw_parent_kids"."kid_id" in (?) | [7 5]
insert into "pw_parent_kids" ("kid_id", "parent_id") values (?, ?) | [5 7]
## toggle.sixAndFive result=&{[5] [6] []} err=<nil>
select "kid_id" from "pw_parent_kids" where "pw_parent_kids"."parent_id" = ? | [7]
delete from "pw_parent_kids" where "pw_parent_kids"."parent_id" = ? and "pw_parent_kids"."kid_id" in (?) | [7 6]
select "kid_id" from "pw_parent_kids" where "pw_parent_kids"."parent_id" = ? and "pw_parent_kids"."kid_id" in (?) | [7 5]
insert into "pw_parent_kids" ("kid_id", "parent_id") values (?, ?) | [5 7]
## syncWithPivot.five result=&{[5] [6] []} err=<nil>
select "kid_id" from "pw_parent_kids" where "pw_parent_kids"."parent_id" = ? | [7]
delete from "pw_parent_kids" where "pw_parent_kids"."parent_id" = ? and "pw_parent_kids"."kid_id" in (?) | [7 6]
select "kid_id" from "pw_parent_kids" where "pw_parent_kids"."parent_id" = ? and "pw_parent_kids"."kid_id" in (?) | [7 5]
insert into "pw_parent_kids" ("kid_id", "parent_id", "role") values (?, ?, ?) | [5 7 x]
## syncWithPivotValues.five result=&{[5] [6] []} err=<nil>
select "kid_id" from "pw_parent_kids" where "pw_parent_kids"."parent_id" = ? | [7]
delete from "pw_parent_kids" where "pw_parent_kids"."parent_id" = ? and "pw_parent_kids"."kid_id" in (?) | [7 6]
select "kid_id" from "pw_parent_kids" where "pw_parent_kids"."parent_id" = ? and "pw_parent_kids"."kid_id" in (?) | [7 5]
insert into "pw_parent_kids" ("kid_id", "parent_id", "role") values (?, ?, ?) | [5 7 x]
## syncWithoutDetachingWithPivot.five result=&{[5] [] []} err=<nil>
select "kid_id" from "pw_parent_kids" where "pw_parent_kids"."parent_id" = ? | [7]
select "kid_id" from "pw_parent_kids" where "pw_parent_kids"."parent_id" = ? and "pw_parent_kids"."kid_id" in (?) | [7 5]
insert into "pw_parent_kids" ("kid_id", "parent_id", "role") values (?, ?, ?) | [5 7 x]
## toggleWithPivot.sixAndFive result=&{[5] [6] []} err=<nil>
select "kid_id" from "pw_parent_kids" where "pw_parent_kids"."parent_id" = ? | [7]
delete from "pw_parent_kids" where "pw_parent_kids"."parent_id" = ? and "pw_parent_kids"."kid_id" in (?) | [7 6]
select "kid_id" from "pw_parent_kids" where "pw_parent_kids"."parent_id" = ? and "pw_parent_kids"."kid_id" in (?) | [7 5]
insert into "pw_parent_kids" ("kid_id", "parent_id", "role") values (?, ?, ?) | [5 7 x]
`
