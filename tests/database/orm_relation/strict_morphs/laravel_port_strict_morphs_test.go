package strict_morphs

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gradii/framework/database/gorm/relations"
	"github.com/gradii/framework/database/orm"
	"github.com/gradii/framework/database/orm/morphmap"
	"github.com/gradii/framework/errors"
)

// This file ports Laravel's tests/Database/DatabaseEloquentStrictMorphsTest.php (12.x) onto the
// framework's morph registry, database/orm/morphmap: one Go test per Laravel test method, named
// TestStrictMorphs_<method without the test prefix>.
//
// The corpus is a DB-less PHPUnit TestCase: no connection is opened, setUp() turns strict mode on
// (Relation::requireMorphMap(), :13-18), and tearDown() clears the morph map and the flag
// (:71-77) — no schema exists for a port to create. The corpus's observable is the value
// Model::getMorphClass() resolves for a model (HasRelationships.php:1006), whose Go counterpart
// is morphmap.MorphValue. Strict mode is ported as the process-level flag of orm.RequireMorphMap /
// orm.RequiresMorphMap / orm.EnforceMorphMap (Relation::requireMorphMap / requiresMorphMap /
// enforceMorphMap — Relation.php:437/:447/:459); with the flag on, resolving an unmapped model
// returns errors.OrmClassMorphViolation — the port of
// Illuminate\Database\ClassMorphViolationException ("No morph map defined for model [X].",
// ClassMorphViolationException.php:24) — and the base relations.Pivot stays exempt
// (HasRelationships.php:1014). All five tests are ported in full; none compiles SQL, so this file
// pins no SQL strings.
//
// Everything this file says about Laravel was measured with the oracle
// (php .claude/laravel-oracle/oracle.php; illuminate/database v12.55.1), and re-measured against
// the user checkout (oracle_checkout.php, 12.43.1) with byte-identical output. Probe:
// /tmp/strictmorphs-probes/strict_morphs_probe.php, quoted per test below:
//
//   - fresh process: requiresMorphMap() false; requireMorphMap() flips it true
//   - requireMorphMap() + an unmapped model throws ClassMorphViolationException
//   - morphMap(['test' => TestModel::class]) resolves the model to 'test'
//   - requireMorphMap(false) + enforceMorphMap(['test' => TestModel::class]) resolves it to 'test'
//     with requiresMorphMap() back true
//   - requireMorphMap() + new Pivot() returns 'Illuminate\Database\Eloquent\Relations\Pivot'
//   - requireMorphMap() + a Pivot subclass throws; the subclass registered in the map resolves to
//     its alias instead

// strictMorphsTestModel ports the corpus's bare `class TestModel extends Model {}`: a model with
// no MorphClass() override and no morph-map entry of its own.
type strictMorphsTestModel struct {
	ID uint64 `gorm:"column:id;primaryKey;autoIncrement"`
}

func (strictMorphsTestModel) TableName() string { return "test_models" }

// strictMorphsTestPivot ports the corpus's `class TestPivotModel extends Pivot {}` (:84-86): a
// distinct Go type embedding relations.Pivot — embedding changes the type identity exactly as
// extends changes static::class, so the base Pivot's exemption does not reach it.
type strictMorphsTestPivot struct {
	relations.Pivot
}

// setUpStrictMorphs is the port of the corpus's setUp/tearDown pair: setUp turns strict mode on
// for every test (:13-18) and tearDown clears the map and the flag (:71-77). The Go registry is
// process-wide, so the cleanup restores both instead of relying on a fresh process per test.
func setUpStrictMorphs(t *testing.T) {
	t.Helper()

	morphmap.Reset()
	orm.RequireMorphMap()
	t.Cleanup(func() {
		morphmap.Reset()
		orm.RequireMorphMap(false)
	})
}

// goClassName is the Go reading of PHP's get_class for the corpus models — package path plus type
// name, the shape errors.OrmClassMorphViolation renders. Computed here independently of
// morphmap's own rendering so the assertions pin the format.
func goClassName(model any) string {
	typ := reflect.TypeOf(model)
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	return typ.PkgPath() + "." + typ.Name()
}

func TestStrictMorphs_StrictModeThrowsAnExceptionOnClassMap(t *testing.T) {
	setUpStrictMorphs(t)

	// Laravel: $model->getMorphClass() throws ClassMorphViolationException. Oracle, executed:
	// 'strict unmapped TestModel getMorphClass threw => Illuminate\Database\ClassMorphViolation-
	// Exception: No morph map defined for model [TestModel].'; the exception's ->model property is
	// the model's class name, which the Go error renders as package path plus type name.
	_, _, err := morphmap.MorphValue(&strictMorphsTestModel{})
	require.Error(t, err)
	assert.ErrorIs(t, err, errors.OrmClassMorphViolation)
	assert.Equal(t, "No morph map defined for model ["+goClassName(&strictMorphsTestModel{})+"].", err.Error())
}

func TestStrictMorphs_StrictModeDoesNotThrowExceptionWhenMorphMap(t *testing.T) {
	setUpStrictMorphs(t)

	// Laravel: Relation::morphMap(['test' => TestModel::class]) — the map hit resolves before the
	// strict check (HasRelationships.php:1011-1013). Oracle, executed: 'strict mapped TestModel
	// getMorphClass => 'test''.
	morphmap.Register(map[string]any{"test": &strictMorphsTestModel{}})

	alias, ok, err := morphmap.MorphValue(&strictMorphsTestModel{})
	require.NoError(t, err)
	require.True(t, ok, "a model registered in the morph map must resolve to its alias")
	assert.Equal(t, "test", alias)
}

func TestStrictMorphs_MapsCanBeEnforcedInOneMethod(t *testing.T) {
	setUpStrictMorphs(t)

	// Laravel: Relation::requireMorphMap(false); Relation::enforceMorphMap(['test' =>
	// TestModel::class]) — strict mode back on plus the map in one call (Relation.php:459-464).
	// Oracle, executed: 'after enforce requiresMorphMap => true' and 'strict mapped TestModel
	// getMorphClass => 'test''.
	orm.RequireMorphMap(false)
	orm.EnforceMorphMap(map[string]any{"test": &strictMorphsTestModel{}})

	assert.True(t, orm.RequiresMorphMap(), "enforceMorphMap must turn strict mode back on (:461)")
	alias, ok, err := morphmap.MorphValue(&strictMorphsTestModel{})
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "test", alias)
}

func TestStrictMorphs_MapIgnoreGenericPivotClass(t *testing.T) {
	setUpStrictMorphs(t)

	// Laravel: new Pivot()->getMorphClass() neither throws nor consults the map — the
	// `static::class === Pivot::class` carve-out returns the base class name
	// (HasRelationships.php:1014-1016). Oracle, executed: 'strict base Pivot getMorphClass =>
	// 'Illuminate\Database\Eloquent\Relations\Pivot''.
	value, ok, err := morphmap.MorphValue(&relations.Pivot{})
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, goClassName(&relations.Pivot{}), value)
}

func TestStrictMorphs_MapCanBeEnforcedToCustomPivotClass(t *testing.T) {
	setUpStrictMorphs(t)

	// Laravel: the carve-out is FQCN-only, so a Pivot subclass must throw. Oracle, executed:
	// 'strict custom pivot unmapped threw => Illuminate\Database\ClassMorphViolationException:
	// No morph map defined for model [TestPivotModel].'; the mapped variant resolves instead:
	// 'strict custom pivot mapped => 'probe''.
	_, _, err := morphmap.MorphValue(&strictMorphsTestPivot{})
	require.Error(t, err)
	assert.ErrorIs(t, err, errors.OrmClassMorphViolation)
	assert.Equal(t, "No morph map defined for model ["+goClassName(&strictMorphsTestPivot{})+"].", err.Error())

	morphmap.Register(map[string]any{"probe": &strictMorphsTestPivot{}})
	alias, ok, err := morphmap.MorphValue(&strictMorphsTestPivot{})
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "probe", alias)
}
