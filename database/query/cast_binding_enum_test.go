package query

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// This file ports Laravel's testAddBindingWithEnum (tests/Database/DatabaseQueryBuilderTest.php
// L5333-5339) and pins the rest of Builder::castBinding(), which passes an enum case through
// enum_value(): a BackedEnum binds as its backing value and a pure (UnitEnum) case as its name.
// Laravel calls it from addBinding and cleanBindings only, so the cast reaches where, having,
// whereIn, whereBetween, insert and update bindings, while setBindings and mergeWheres store their
// values as given. Go has no enum construct, so a type declares itself an enum by implementing
// BackedEnum or UnitEnum. Every expectation below was confirmed against the oracle at
// .claude/laravel-oracle/oracle.php (illuminate/database v12.55.1), using the corpus's own enums.

// statusNames holds the case names shared by the fixtures below: draft, pending, done.
var statusNames = [...]string{"draft", "pending", "done"}

// integerStatus is the Go counterpart of the corpus fixture `enum IntegerStatus: int`
// (tests/Database/Enums.php): a backed enum whose cases carry an int. Like every PHP backed enum it
// also has case names, which is what makes the value-over-name precedence observable.
type integerStatus int

const (
	integerStatusDraft integerStatus = iota
	integerStatusPending
	integerStatusDone
)

func (s integerStatus) EnumName() string { return statusNames[s] }
func (s integerStatus) EnumValue() any   { return int(s) }

// stringStatus is the counterpart of `enum StringStatus: string`: a backed enum whose cases carry a
// string.
type stringStatus string

const (
	stringStatusDraft stringStatus = "draft"
	stringStatusDone  stringStatus = "done"
)

func (s stringStatus) EnumName() string { return string(s) }
func (s stringStatus) EnumValue() any   { return string(s) }

// nonBackedStatus is the counterpart of `enum NonBackedStatus`: a pure enum, identified by name
// alone. It is represented by an int, which is what database/sql would bind (its ordinal) if
// CastBinding passed it through unchanged.
type nonBackedStatus int

const (
	nonBackedStatusDraft nonBackedStatus = iota
	nonBackedStatusPending
	nonBackedStatusDone
)

func (s nonBackedStatus) EnumName() string { return statusNames[s] }

var (
	_ BackedEnum = integerStatusDone
	_ UnitEnum   = integerStatusDone
	_ BackedEnum = stringStatusDone
	_ UnitEnum   = nonBackedStatusDone
)

// TestAddBindingWithEnum ports Laravel's testAddBindingWithEnum (corpus L5333-5339): a backed enum is
// bound as its backing value, and a pure enum, passed inside a list, as its name.
func TestAddBindingWithEnum(t *testing.T) {
	builder := newTestBuilder()
	assert.NoError(t, builder.AddBinding(integerStatusDone, "where"))
	assert.NoError(t, builder.AddBinding([]any{nonBackedStatusDone}, "where"))

	assert.Equal(t, []any{2, "done"}, builder.GetBindings())
}

// TestEnumBindingsThroughTheEntryPoints pins that the cast reaches the bindings the entry points
// produce, through AddBinding (where, having) and CleanBindings (whereIn, whereBetween, insert,
// update). These are the two places Laravel calls castBinding.
func TestEnumBindingsThroughTheEntryPoints(t *testing.T) {
	t.Run("where and whereIn", func(t *testing.T) {
		sql, bindings := newTestBuilder().From("users", "").
			Where("status", integerStatusDone).
			WhereIn("kind", []any{stringStatusDraft, nonBackedStatusPending}, BooleanAnd, false).
			ToSelectSQL()

		assert.Equal(t, `select * from "users" where "status" = ? and "kind" in (?, ?)`, sql)
		assert.Equal(t, []any{2, "draft", "pending"}, bindings)
	})

	t.Run("whereBetween", func(t *testing.T) {
		sql, bindings := newTestBuilder().From("users", "").
			WhereBetween("status", []any{integerStatusDraft, integerStatusDone}, BooleanAnd, false).
			ToSelectSQL()

		assert.Equal(t, `select * from "users" where "status" between ? and ?`, sql)
		assert.Equal(t, []any{0, 2}, bindings)
	})

	t.Run("having", func(t *testing.T) {
		sql, bindings := newTestBuilder().From("users", "").
			Having("status", "=", integerStatusPending).
			ToSelectSQL()

		assert.Equal(t, `select * from "users" having "status" = ?`, sql)
		assert.Equal(t, []any{1}, bindings)
	})

	t.Run("insert", func(t *testing.T) {
		sql, bindings := newTestBuilder().From("users", "").
			ToInsertSQL([]map[string]any{{"kind": nonBackedStatusPending, "status": integerStatusDone}})

		assert.Equal(t, `insert into "users" ("kind", "status") values (?, ?)`, sql)
		assert.Equal(t, []any{"pending", 2}, bindings)
	})

	t.Run("update", func(t *testing.T) {
		sql, bindings := newTestBuilder().From("users", "").Where("id", 1).
			ToUpdateSQL(map[string]any{"status": stringStatusDone})

		assert.Equal(t, `update "users" set "status" = ? where "id" = ?`, sql)
		assert.Equal(t, []any{"done", 1}, bindings)
	})
}

// TestCastBinding pins castBinding() itself. A type implementing both interfaces binds its value,
// because PHP's BackedEnum extends UnitEnum and enum_value() tests it first. A pointer to a case is
// unwrapped like the case. Every value that is not an enum keeps its dynamic type. That includes a
// named integer that does not declare itself an enum, and the uint key that
// database/gorm/sql_test.go's TestCompileUpdate_ScopesToTheModelKey binds as uint(7).
func TestCastBinding(t *testing.T) {
	assert.Equal(t, 2, CastBinding(integerStatusDone))
	assert.Equal(t, "done", CastBinding(stringStatusDone))
	assert.Equal(t, "pending", CastBinding(nonBackedStatusPending))

	done, pending := integerStatusDone, nonBackedStatusPending
	assert.Equal(t, 2, CastBinding(&done))
	assert.Equal(t, "pending", CastBinding(&pending))

	type userID int
	for _, value := range []any{nil, 5, "x", uint(7), userID(7), time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC), []byte("raw"), Expression{Value: "now()"}} {
		assert.Equal(t, value, CastBinding(value))
	}
}

// TestCastBindingLeavesANilEnumPointerAlone pins that a nil pointer to an enum type is returned
// unchanged rather than unwrapped. Calling a value-receiver method through it would panic, and
// database/sql binds the nil pointer as NULL, which is what castBinding(null) yields.
func TestCastBindingLeavesANilEnumPointerAlone(t *testing.T) {
	var backed *integerStatus
	var pure *nonBackedStatus

	assert.NotPanics(t, func() {
		assert.Equal(t, backed, CastBinding(backed))
		assert.Equal(t, pure, CastBinding(pure))
	})

	// The same holds on the paths that cast, so a nil optional enum column can still be inserted.
	_, bindings := newTestBuilder().From("users", "").ToInsertSQL([]map[string]any{{"status": backed}})
	assert.Equal(t, []any{backed}, bindings)
}

// TestSetBindingsAndMergeWheresStoreEnumsAsGiven pins the paths where Laravel does not cast:
// setBindings and mergeWheres store their values as given. A later addBinding of a list re-casts what
// is already stored, because Laravel maps castBinding over the merged list. A scalar addBinding casts
// only the value it appends.
func TestSetBindingsAndMergeWheresStoreEnumsAsGiven(t *testing.T) {
	values := []any{integerStatusDone, nonBackedStatusDone}

	set := newTestBuilder()
	assert.NoError(t, set.SetBindings(values, "where"))
	assert.Equal(t, []any{integerStatusDone, nonBackedStatusDone}, set.GetBindings())

	// The builder keeps its own copy, so a later change to the caller's slice does not reach it.
	values[0] = "changed"
	assert.Equal(t, []any{integerStatusDone, nonBackedStatusDone}, set.GetBindings())

	merged := newTestBuilder().MergeWheres(nil, []any{integerStatusDone, nonBackedStatusDone})
	assert.Equal(t, []any{integerStatusDone, nonBackedStatusDone}, merged.GetBindings())

	listed := newTestBuilder()
	assert.NoError(t, listed.SetBindings([]any{integerStatusDone}, "where"))
	assert.NoError(t, listed.AddBinding([]any{nonBackedStatusDraft}, "where"))
	assert.Equal(t, []any{2, "draft"}, listed.GetBindings())

	scalar := newTestBuilder()
	assert.NoError(t, scalar.SetBindings([]any{integerStatusDone}, "where"))
	assert.NoError(t, scalar.AddBinding(nonBackedStatusDraft, "where"))
	assert.Equal(t, []any{integerStatusDone, "draft"}, scalar.GetBindings())
}
