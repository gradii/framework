package has_one

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
)

// This file pins the call-time withDefault() chain on the relation object — the item
// lalign-defaults-inverse half Laravel's corpus exercises inline (HasOne::withDefault before
// getResults) rather than as its own test method. The Go form is the WithDefault method on the
// object Related(...) returns (contracts.HasOneRelation), the one slot
// SupportsDefaultModels::$withDefault carries (Relations/Concerns/SupportsDefaultModels.php:32):
// the declaration seeds it, the call replaces it. Every behavior was measured by the oracle
// against illuminate/database v12.55.1 (php .claude/laravel-oracle/oracle.php
// /tmp/lalign-defaults-inverse/runtime_withdefault.php): hasOne.missing-row.attributes
// {"foreign_key":2}, hasOne.null-key.queries 0, hasOne.declared-then-runtime.name "runtime"; the
// no-query short-circuit on a null parent key is pinned statement-level in database/gorm's
// defaults_inverse_engine_test.go.
func TestHasOne_HasOneWithDefaultRuntimeChaining(t *testing.T) {
	query := newRelationQuery(t)
	hasOneSchema(t, query)

	user := hasOneSeedUser(t, query)

	// Laravel: $user->phone()->withDefault()->getResults() — a miss materialises the fresh related
	// instance with the FK pre-set to the parent key; nothing is persisted.
	rel := cloneRelationQuery(t, query).Related(&user, "Phone").(contractsorm.HasOneRelation)
	rel.WithDefault(true)
	var phone hasOnePhone
	require.NoError(t, rel.GetResults(&phone))
	assert.Equal(t, user.ID, phone.ForeignKey)
	assert.Empty(t, phone.Name)
	assert.Zero(t, phone.ID, "the default is never persisted")

	// The call-time slot replaces a declared default (oracle: hasOne.declared-then-runtime.name
	// "runtime"); without the call the declaration still applies.
	declared := hasOneDefaultUser{Username: "declared"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&declared))

	relRuntime := cloneRelationQuery(t, query).Related(&declared, "Phone").(contractsorm.HasOneRelation)
	relRuntime.WithDefault(map[string]any{"name": "runtime"})
	var runtimePhone hasOnePhone
	require.NoError(t, relRuntime.GetResults(&runtimePhone))
	assert.Equal(t, "runtime", runtimePhone.Name)
	assert.Equal(t, declared.ID, runtimePhone.ForeignKey)

	var declaredPhone hasOnePhone
	require.NoError(t, cloneRelationQuery(t, query).Related(&declared, "Phone").GetResults(&declaredPhone))
	assert.Equal(t, declared.ID, declaredPhone.ForeignKey)
	assert.Empty(t, declaredPhone.Name, "the declaration's default carries no attributes")

	// A real row always wins over the call-time default.
	existing := hasOnePhone{ForeignKey: user.ID, Name: "phone-1"}
	require.NoError(t, cloneRelationQuery(t, query).Create(&existing))

	relHit := cloneRelationQuery(t, query).Related(&user, "Phone").(contractsorm.HasOneRelation)
	relHit.WithDefault(map[string]any{"name": "runtime"})
	var hit hasOnePhone
	require.NoError(t, relHit.GetResults(&hit))
	assert.Equal(t, "phone-1", hit.Name)
	assert.Equal(t, existing.ID, hit.ID)
}
