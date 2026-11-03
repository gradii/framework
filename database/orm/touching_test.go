package orm

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The fixtures mirror the corpus's touching models: touchingTestUser and touchingTestPost embed
// the framework base model, the Go form of `extends Eloquent`, so ignoring the base class
// ignores them and ignoring one of them leaves the other alone.

type touchingTestUser struct {
	Model

	Email string
}

type touchingTestPost struct {
	Model

	Name string
}

// touchingTestNoTimestamps is the shape Model::isIgnoringTouch reports true for without any
// scope: a model carrying no updated-at column (Eloquent's UPDATED_AT null / $timestamps false).
type touchingTestNoTimestamps struct {
	Name string
}

func TestIsIgnoringTouch_ModelWithUpdatedAtIsTouchable(t *testing.T) {
	assert.False(t, IsIgnoringTouch(&touchingTestUser{}))
	assert.False(t, IsIgnoringTouch(&touchingTestPost{}))
	assert.False(t, IsIgnoringTouch(touchingTestUser{}))
}

func TestIsIgnoringTouch_ModelWithoutUpdatedAtIgnoresTouches(t *testing.T) {
	assert.True(t, IsIgnoringTouch(&touchingTestNoTimestamps{}))
}

func TestWithoutTouchingOn_IgnoresTheGivenClassAndRestoresIt(t *testing.T) {
	assert.False(t, IsIgnoringTouch(&touchingTestUser{}))

	require.NoError(t, WithoutTouchingOn([]any{&touchingTestUser{}}, func() error {
		assert.True(t, IsIgnoringTouch(&touchingTestUser{}))

		// The sibling class is not a child of the ignored one, so its touches stay enabled —
		// the corpus's testChildModelsAreIgnored observable.
		assert.False(t, IsIgnoringTouch(&touchingTestPost{}))

		return nil
	}))

	assert.False(t, IsIgnoringTouch(&touchingTestUser{}))
}

func TestWithoutTouching_IgnoresTheBaseModelAndAllItsSubclasses(t *testing.T) {
	require.NoError(t, WithoutTouching(func() error {
		assert.True(t, IsIgnoringTouch(&Model{}))

		// Every fixture embeds Model — the Go is_subclass_of — so all of them are ignored: the
		// corpus's testWhenBaseModelIsIgnoredAllChildModelsAreIgnored observable.
		assert.True(t, IsIgnoringTouch(&touchingTestUser{}))
		assert.True(t, IsIgnoringTouch(&touchingTestPost{}))

		return nil
	}))

	assert.False(t, IsIgnoringTouch(&Model{}))
	assert.False(t, IsIgnoringTouch(&touchingTestUser{}))
	assert.False(t, IsIgnoringTouch(&touchingTestPost{}))
}

func TestWithoutTouchingOn_RestoresTheStateWhenTheCallbackErrors(t *testing.T) {
	expected := errors.New("callback failed")

	err := WithoutTouchingOn([]any{&touchingTestUser{}}, func() error {
		assert.True(t, IsIgnoringTouch(&touchingTestUser{}))
		return expected
	})

	require.ErrorIs(t, err, expected)
	assert.False(t, IsIgnoringTouch(&touchingTestUser{}))
}

func TestWithoutTouchingOn_NestedScopesRestoreEachLayer(t *testing.T) {
	require.NoError(t, WithoutTouchingOn([]any{&touchingTestUser{}}, func() error {
		require.NoError(t, WithoutTouchingOn([]any{&touchingTestPost{}}, func() error {
			assert.True(t, IsIgnoringTouch(&touchingTestUser{}))
			assert.True(t, IsIgnoringTouch(&touchingTestPost{}))
			return nil
		}))

		assert.True(t, IsIgnoringTouch(&touchingTestUser{}))
		assert.False(t, IsIgnoringTouch(&touchingTestPost{}))

		return nil
	}))

	assert.False(t, IsIgnoringTouch(&touchingTestUser{}))
	assert.False(t, IsIgnoringTouch(&touchingTestPost{}))
}

func TestWithoutTouchingOn_RestoreDropsEveryCopyOfTheClass(t *testing.T) {
	// Eloquent restores with array_diff, which removes every occurrence of the entered classes; a
	// class entered by an outer and a nested scope of the same class is therefore fully removed
	// when the inner scope ends. The Go restore mirrors the array_diff.
	require.NoError(t, WithoutTouchingOn([]any{&touchingTestUser{}}, func() error {
		require.NoError(t, WithoutTouchingOn([]any{&touchingTestUser{}}, func() error {
			assert.True(t, IsIgnoringTouch(&touchingTestUser{}))
			return nil
		}))

		assert.False(t, IsIgnoringTouch(&touchingTestUser{}))

		return nil
	}))

	assert.False(t, IsIgnoringTouch(&touchingTestUser{}))
}

// touchingTestUnexportedBase is embedded UNEXPORTED in the child below: the shape whose
// updated-at column the field walk must still find. embedsType already walks unexported
// anonymous fields; carriesUpdatedAt's old export gate skipped them, so the child read
// IsIgnoringTouch()==true for the wrong reason ("carries no updated-at column").
type touchingTestUnexportedBase struct {
	Model
}

type touchingTestChildOfUnexportedBase struct {
	touchingTestUnexportedBase

	Name string
}

func TestIsIgnoringTouch_ModelEmbeddingAnUnexportedBaseIsTouchable(t *testing.T) {
	// Pre-scope: the child carries Model's updated-at column through the unexported embed, so
	// its touches are NOT ignored.
	assert.False(t, IsIgnoringTouch(&touchingTestChildOfUnexportedBase{}))

	// In-scope: ignoring the embedded (unexported) class still ignores the child - the Go form
	// of is_subclass_of over the same embed walk.
	require.NoError(t, WithoutTouchingOn([]any{&touchingTestUnexportedBase{}}, func() error {
		assert.True(t, IsIgnoringTouch(&touchingTestChildOfUnexportedBase{}))

		return nil
	}))

	assert.False(t, IsIgnoringTouch(&touchingTestChildOfUnexportedBase{}))
}
