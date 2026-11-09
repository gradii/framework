package morph_one_of_many

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The ported ofMany fixtures are declared the way Laravel's tests declare them - the array form with
// lowercase aggregates, the closure, a structured join - so each relation renders Laravel's own
// statement. Nothing else asserted these fixtures' SQL, which let an OfMany(col, "MAX") declaration and
// a raw join string drift from Laravel unnoticed. Every expectation is Laravel's toSql() for the
// same relation (oracle, illuminate/database v12.55.1, sqlite,
// /Users/dog/.claude/jobs/013c27a0/tmp/ofm/fixture_sql_probe.php -> fixture_sql_probe.out).

func TestMorphOneOfMany_ClosureFixtureRendersLaravelSQL(t *testing.T) {
	product := morphOneOfManyProduct{ID: 1}
	assert.Equal(t, `select "states".* from "states" inner join (select max("states"."id") as "id_aggregate", "states"."stateful_id", "states"."stateful_type" from "states" where "type" = ? and "states"."stateful_type" = ? and "states"."stateful_id" = ? and "states"."stateful_id" is not null group by "states"."stateful_id", "states"."stateful_type") as "current_foo_state" on "current_foo_state"."id_aggregate" = "states"."id" and "current_foo_state"."stateful_type" = "states"."stateful_type" and "current_foo_state"."stateful_id" = "states"."stateful_id" where "states"."stateful_type" = ? and "states"."stateful_id" = ? and "states"."stateful_id" is not null`,
		newRelationQuery(t).Related(&product, "CurrentFooState").ToSql().Get(&[]morphOneOfManyState{}))
}
