package relations

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/gradii/framework/errors"
)

// The descriptor seam (descriptor.go) is a package-level struct of function fields that
// database/gorm installs from its init() through UseDescriptorSeam. Its zero value is all nil, so
// before that init — a relations-only caller, or a test binary that never links database/gorm —
// every resolver entry point used to call a nil function and panic (followup-descriptorseam-guard,
// database/tmp/ORM_BUILDER_PLAN.md FOLLOW-UP CANDIDATE 8). These tests pin the guard from inside
// the package, where the seam variable can be zeroed deterministically: the test binary links
// database/gorm through the relations_test files, so the seam is normally installed here and each
// test restores it. The guard has no Laravel counterpart (Eloquent's resolver is a class hierarchy
// linked at load time), so there is nothing to probe in the oracle: it is a Go-only safety
// invariant and the installed path must be byte-for-byte the old behavior.

// seamGuardParent is a harmless parent value: the entry points must report the absent seam before
// they ever look at it.
type seamGuardParent struct{ ID uint }

func TestDescriptorSeam_AbsentEntryPointsReturnNamedError(t *testing.T) {
	saved := descriptorSeam
	descriptorSeam = DescriptorSeam{}
	t.Cleanup(func() { descriptorSeam = saved })

	require.False(t, descriptorSeamInstalled())
	parent := &seamGuardParent{ID: 1}

	// Every exported entry point that reads a seam member returns the named error instead of
	// dereferencing a nil reader.
	_, err := ResolveRelation(parent, "Children")
	require.ErrorIs(t, err, errors.OrmRelationDescriptorSeamMissing)

	_, err = ResolveRelation(parent, "Children.Tags")
	require.ErrorIs(t, err, errors.OrmRelationDescriptorSeamMissing)

	_, err = DescriptorFromMorphOneOrMany(parent, "seam_guard_parents", "Images", &struct{}{}, "imageable", "", "", "id", KindMorphOne)
	require.ErrorIs(t, err, errors.OrmRelationDescriptorSeamMissing)

	_, err = BuildMorphPivotDescriptor(parent, "seam_guard_parents", "Tags", &struct{}{}, "taggable", "", "", "parent_id", "tag_id", "id", "id", "", false, false)
	require.ErrorIs(t, err, errors.OrmRelationDescriptorSeamMissing)

	_, _, err = ResolvePivotTimestamps(&struct{}{}, "Pivot", true)
	require.ErrorIs(t, err, errors.OrmRelationDescriptorSeamMissing)

	_, err = ResolveMorphValue(parent, "seam_guard_parents")
	require.ErrorIs(t, err, errors.OrmRelationDescriptorSeamMissing)

	_, _, err = ResolveMorphAlias(parent)
	require.ErrorIs(t, err, errors.OrmRelationDescriptorSeamMissing)

	// Argument validation that never needed the seam still runs first and is unchanged.
	_, err = ResolveRelation(parent, "")
	require.ErrorIs(t, err, errors.OrmQueryEmptyRelation)
	_, err = DescriptorFromMorphOneOrMany(parent, "seam_guard_parents", "Images", nil, "imageable", "", "", "id", KindMorphOne)
	require.ErrorIs(t, err, errors.OrmMorphRelationMissingField)
	_, err = BuildMorphPivotDescriptor(parent, "seam_guard_parents", "Tags", &struct{}{}, "", "", "", "parent_id", "tag_id", "id", "id", "", false, false)
	require.ErrorIs(t, err, errors.OrmMorphRelationMissingField)
}

func TestDescriptorSeam_PartialSeamCountsAsAbsent(t *testing.T) {
	saved := descriptorSeam
	t.Cleanup(func() { descriptorSeam = saved })

	// A seam missing any one reader is not installed: the resolver would still hit a nil function
	// on the path that needs the missing member.
	descriptorSeam = DescriptorSeam{
		ParseModel: func(model any) (ModelSchema, error) { return nil, nil },
		TableOf:    func(model any) (string, error) { return "", nil },
	}
	require.False(t, descriptorSeamInstalled())
	_, err := ResolveRelation(&seamGuardParent{ID: 1}, "Children")
	require.ErrorIs(t, err, errors.OrmRelationDescriptorSeamMissing)
}

func TestDescriptorSeam_InstalledSeamIsTransparent(t *testing.T) {
	saved := descriptorSeam
	t.Cleanup(func() { descriptorSeam = saved })

	// With every reader installed the guard steps aside and the readers' own results surface
	// unchanged — here a sentinel error from each reader the entry point consults first.
	sentinel := errors.New("descriptor seam reader sentinel")
	descriptorSeam = DescriptorSeam{
		ParseModel: func(model any) (ModelSchema, error) { return nil, sentinel },
		TableOf:    func(model any) (string, error) { return "", sentinel },
		PivotFieldStructType: func(relatedModel any, pivotFieldName string) (reflect.Type, bool) {
			return nil, false
		},
		HasRelationTag: func(model any, name string) bool { return false },
		MorphValue:     func(model any) (string, bool, error) { return "", false, sentinel },
	}
	require.True(t, descriptorSeamInstalled())
	parent := &seamGuardParent{ID: 1}

	_, err := ResolveRelation(parent, "Children")
	require.ErrorIs(t, err, sentinel)
	_, err = DescriptorFromMorphOneOrMany(parent, "seam_guard_parents", "Images", &struct{}{}, "imageable", "", "", "id", KindMorphOne)
	require.ErrorIs(t, err, sentinel)
	_, err = BuildMorphPivotDescriptor(parent, "seam_guard_parents", "Tags", &struct{}{}, "taggable", "", "", "parent_id", "tag_id", "id", "id", "", false, false)
	require.ErrorIs(t, err, sentinel)
	_, err = ResolveMorphValue(parent, "seam_guard_parents")
	require.ErrorIs(t, err, sentinel)
	_, _, err = ResolveMorphAlias(parent)
	require.ErrorIs(t, err, sentinel)

	// PivotFieldStructType reports no pivot struct, so the fallback columns resolve as before.
	created, updated, err := ResolvePivotTimestamps(&struct{}{}, "Pivot", true)
	require.NoError(t, err)
	require.Equal(t, "created_at", created)
	require.Equal(t, "updated_at", updated)
}
