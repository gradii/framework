package belongs_to_many_with_default_attributes

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
)

// TestBelongsToManyWithDefaultAttributes_WithPivotValueStampsTheOrFailFamily pins the transaction
// arm of the recorded-pair threading: attachOrFail / syncOrFail / toggleOrFail re-dispatch inside
// the parent-connection session (IWPT::attachOrFail :365, ::syncOrFail :157, ::toggleOrFail :75),
// so the recorded list must survive the transaction and still stamp the pivot INSERT
// (baseAttachRecord's values loop, IWPT :454-472). Oracle v12.55.1, executed,
// /tmp/r7pv/attach_pv_probe.php A4: attachOrFail after withPivotValue('is_admin','gold') writes the
// same INSERT as the non-fail form. Each verb lands on its own club so the three writes do not
// detach one another.
func TestBelongsToManyWithDefaultAttributes_WithPivotValueStampsTheOrFailFamily(t *testing.T) {
	query := newRelationQuery(t)
	belongsToManyWithDefaultAttributesSchema(t, query)

	for _, id := range []uint{1, 2, 3} {
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

	require.NoError(t, mustRelation(1).AttachOrFail([]any{uint(1)}))
	_, err := mustRelation(2).SyncOrFail([]any{uint(2)})
	require.NoError(t, err)
	_, err = mustRelation(3).ToggleOrFail([]any{uint(3)})
	require.NoError(t, err)

	rows := []belongsToManyWithDefaultAttributesPivotRow{}
	require.NoError(t, cloneRelationQuery(t, query).Model(&belongsToManyWithDefaultAttributesPivotRow{}).Get(&rows))
	require.Len(t, rows, 3)
	for _, row := range rows {
		require.NotNil(t, row.IsAdmin, "club %d user %d lost the recorded pivot value", row.ClubID, row.UserID)
		assert.Equal(t, 1, *row.IsAdmin)
		assert.Equal(t, row.ClubID, row.UserID)
	}
}
