package gorm

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gradii/framework/database/gorm/relations"
	"github.com/gradii/framework/database/orm/morphmap"
	"github.com/gradii/framework/errors"
)

// This file pins the strict-morph wiring of the engine: resolveMorphValue — the descriptor-time
// resolution behind every polymorphic read filter and *_type write stamp — and resolveMorphAlias,
// the MorphTo associate path. Both surface the registry's strict-mode error
// (errors.OrmClassMorphViolation, the port of Illuminate\Database\ClassMorphViolationException)
// once orm.RequireMorphMap / orm.EnforceMorphMap turned the process-level flag on; while off,
// resolveMorphValue keeps today's table-name fallback. The DB-less corpus in
// tests/database/orm_relation/strict_morphs ports Laravel's DatabaseEloquentStrictMorphsTest
// against the registry itself; this file covers the relation-descriptor and associate wiring.
// Shared behavior receipts: oracle probe /tmp/strictmorphs-probes/strict_morphs_probe.php
// (illuminate/database v12.55.1) — 'strict unmapped TestModel getMorphClass threw =>
// Illuminate\Database\ClassMorphViolationException: No morph map defined for model [TestModel].';
// 'strict base Pivot getMorphClass => 'Illuminate\\Database\\Eloquent\\Relations\\Pivot'';
// 'strict base MorphPivot threw' the same violation.

type strictMorphUnmapped struct {
	ID uint64
}

func (strictMorphUnmapped) TableName() string { return "strict_morph_unmapped" }

type strictMorphRelated struct {
	ID uint64
}

func (strictMorphRelated) TableName() string { return "strict_morph_related" }

// strictMorphClassName renders the expected class name the way the registry does: package path
// plus type name. Computed independently of morphmap's rendering so the assertion pins the format.
func strictMorphClassName(model any) string {
	typ := reflect.TypeOf(model)
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	return typ.PkgPath() + "." + typ.Name()
}

// resetStrictMorphs restores the process-wide registry and the strict flag around one test.
func resetStrictMorphs(t *testing.T) {
	t.Helper()

	morphmap.Reset()
	morphmap.RequireMorphMap(false)
	t.Cleanup(func() {
		morphmap.Reset()
		morphmap.RequireMorphMap(false)
	})
}

func TestResolveMorphValue_LaxModeFallsBackToTableName(t *testing.T) {
	resetStrictMorphs(t)

	value, err := relations.ResolveMorphValue(&strictMorphUnmapped{}, "strict_morph_unmapped")
	require.NoError(t, err)
	assert.Equal(t, "strict_morph_unmapped", value)
}

func TestResolveMorphValue_StrictModeRaisesForUnmappedModel(t *testing.T) {
	resetStrictMorphs(t)
	morphmap.RequireMorphMap()

	value, err := relations.ResolveMorphValue(&strictMorphUnmapped{}, "strict_morph_unmapped")
	require.Error(t, err)
	assert.ErrorIs(t, err, errors.OrmClassMorphViolation)
	assert.Equal(t, "No morph map defined for model ["+strictMorphClassName(&strictMorphUnmapped{})+"].", err.Error())
	assert.Empty(t, value)
}

func TestResolveMorphValue_StrictModeResolvesMappedModel(t *testing.T) {
	resetStrictMorphs(t)
	morphmap.RequireMorphMap()
	morphmap.Register(map[string]any{"strict_morph": &strictMorphUnmapped{}})

	value, err := relations.ResolveMorphValue(&strictMorphUnmapped{}, "strict_morph_unmapped")
	require.NoError(t, err)
	assert.Equal(t, "strict_morph", value)
}

func TestResolveMorphValue_StrictModeExemptsBasePivot(t *testing.T) {
	resetStrictMorphs(t)
	morphmap.RequireMorphMap()

	value, err := relations.ResolveMorphValue(&relations.Pivot{}, "pivots")
	require.NoError(t, err)
	assert.Equal(t, strictMorphClassName(&relations.Pivot{}), value)
}

func TestResolveMorphValue_StrictModeRejectsMorphPivot(t *testing.T) {
	resetStrictMorphs(t)
	morphmap.RequireMorphMap()

	_, err := relations.ResolveMorphValue(&relations.MorphPivot{}, "pivots")
	require.Error(t, err)
	assert.ErrorIs(t, err, errors.OrmClassMorphViolation)
}

func TestResolveMorphAlias_StrictMode(t *testing.T) {
	resetStrictMorphs(t)

	// Lax: no alias and no error — mutateAssociate falls back to the owner's table name.
	alias, ok, err := relations.ResolveMorphAlias(&strictMorphUnmapped{})
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Empty(t, alias)

	// Strict: the associate path raises for the unmapped owner.
	morphmap.RequireMorphMap()
	_, _, err = relations.ResolveMorphAlias(&strictMorphUnmapped{})
	require.Error(t, err)
	assert.ErrorIs(t, err, errors.OrmClassMorphViolation)

	// Registered: the alias wins before the strict check.
	morphmap.Register(map[string]any{"strict_morph": &strictMorphUnmapped{}})
	alias, ok, err = relations.ResolveMorphAlias(&strictMorphUnmapped{})
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "strict_morph", alias)
}

func TestDescriptorFromMorphOneOrMany_StrictModeRaisesAtDeclaration(t *testing.T) {
	resetStrictMorphs(t)

	// Lax: the parent's table name is the descriptor's morph value (today's documented fallback).
	desc, err := relations.DescriptorFromMorphOneOrMany(&strictMorphUnmapped{}, "strict_morph_unmapped", "relateds", &strictMorphRelated{}, "imageable", "", "", "", relKindMorphMany)
	require.NoError(t, err)
	assert.Equal(t, "strict_morph_unmapped", desc.MorphValue)

	// Strict: resolution raises while the descriptor is built — the moment Laravel's
	// MorphOneOrMany constructor calls $parent->getMorphClass() and throws.
	morphmap.RequireMorphMap()
	_, err = relations.DescriptorFromMorphOneOrMany(&strictMorphUnmapped{}, "strict_morph_unmapped", "relateds", &strictMorphRelated{}, "imageable", "", "", "", relKindMorphMany)
	require.Error(t, err)
	assert.ErrorIs(t, err, errors.OrmClassMorphViolation)
}

func TestBuildMorphPivotDescriptor_StrictModeResolvesOnlyTheStampedSide(t *testing.T) {
	resetStrictMorphs(t)
	morphmap.RequireMorphMap()
	morphmap.Register(map[string]any{"strict_morph": &strictMorphRelated{}})

	// Inverse (morphedByMany): Laravel's MorphToMany constructor stamps the RELATED side's morph
	// class and never resolves the owner (MorphToMany.php:71) — oracle v12.55.1, executed: a
	// strict morphedByMany whose owner is unmapped and whose related is mapped resolves the alias
	// without throwing.
	desc, err := relations.BuildMorphPivotDescriptor(&strictMorphUnmapped{}, "strict_morph_unmapped", "relateds", &strictMorphRelated{}, "taggable", "", "", "", "", "", "", "", false, true)
	require.NoError(t, err)
	assert.Equal(t, "strict_morph", desc.MorphValue)

	// The stamped side still raises when it is unmapped: inverse with an unmapped related. Oracle,
	// executed: the same violation names the related model.
	_, err = relations.BuildMorphPivotDescriptor(&strictMorphRelated{}, "strict_morph_related", "relateds", &strictMorphUnmapped{}, "taggable", "", "", "", "", "", "", "", false, true)
	require.Error(t, err)
	assert.ErrorIs(t, err, errors.OrmClassMorphViolation)

	// Forward morphToMany resolves the parent only — an unmapped related does not raise (oracle,
	// executed: a strict morphToMany whose related is unmapped resolves the parent's alias).
	desc, err = relations.BuildMorphPivotDescriptor(&strictMorphRelated{}, "strict_morph_related", "relateds", &strictMorphUnmapped{}, "taggable", "", "", "", "", "", "", "", false, false)
	require.NoError(t, err)
	assert.Equal(t, "strict_morph", desc.MorphValue)
}
