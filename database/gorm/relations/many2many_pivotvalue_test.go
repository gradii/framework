package relations_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
)

// TestMany2Many_WithPivotValueStampsSyncAndToggleInserts closes the coverage the attach-only pins
// leave open: the item threads WithPivotValue's recorded pairs through every attach-consuming
// shell, so the sync and toggle families' INSERTs must carry them too. The pair travels
// SyncCore -> DoAttach -> BasePivotRow, the same values loop the attach path uses
// (baseAttachRecord, IWPT :454-472). Oracle v12.55.1, executed, /tmp/r7pv/attach_pv_probe.php
// A5/A6: sync and toggle after withPivotValue('is_admin','gold') insert with the recorded binding.
func TestMany2Many_WithPivotValueStampsSyncAndToggleInserts(t *testing.T) {
	operations := map[string]func(relation contractsorm.BelongsToManyRelation) error{
		"Sync": func(relation contractsorm.BelongsToManyRelation) error {
			_, err := relation.Sync([]any{uint(9)})

			return err
		},
		"Toggle": func(relation contractsorm.BelongsToManyRelation) error {
			_, err := relation.Toggle([]any{uint(9)})

			return err
		},
	}

	for name, run := range operations {
		t.Run(name, func(t *testing.T) {
			builder, recorder := newRelationTestBuilder(t)

			relation := mmRolesRelation(t, builder, &mmUser{ID: 7}).WithPivotValue("role", "gold")
			require.NoError(t, run(relation))

			inserts := make([]recordedStatement, 0, 1)
			for _, statement := range recorder.take() {
				if strings.HasPrefix(statement.query, `insert into "mm_user_roles"`) {
					inserts = append(inserts, statement)
				}
			}
			require.Len(t, inserts, 1, "the %s family must attach the missing pivot row", name)
			assert.Equal(t, `insert into "mm_user_roles" ("role", "role_id", "user_id") values (?, ?, ?)`, inserts[0].query)
			require.Len(t, inserts[0].args, 3)
			assert.Equal(t, "gold", deref(inserts[0].args[0]))
			assert.EqualValues(t, 9, deref(inserts[0].args[1]))
			assert.EqualValues(t, 7, deref(inserts[0].args[2]))
		})
	}
}
