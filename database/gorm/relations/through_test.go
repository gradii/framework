package relations

import (
	"testing"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
)

// TestHasManyThroughRelation_One_NarrowsTheKind pins the desc-copy conversion of
// HasManyThrough::one(): the narrowed object keeps the parent, the name and the through
// projection, exposes the HasOneThrough surface (and no longer the HasManyThrough one), and its
// GetResults reads through the single-result shape — the FirstRow hook, not the many-result Get.
func TestHasManyThroughRelation_One_NarrowsTheKind(t *testing.T) {
	firstRows := 0
	hooks := Hooks{
		FirstRow: func(query contractsorm.Query, dest any) (bool, error) {
			firstRows++

			return true, nil
		},
	}

	many := Wrap(nil, "parent-model", "children", RelationInfo{
		Kind:           KindHasManyThrough,
		ParentTable:    "parents",
		RelatedTable:   "children",
		ThroughTable:   "pivots",
		SecondKey:      "child_id",
		LocalKey:       "id",
		SecondLocalKey: "id",
	}, hooks).(contractsorm.HasManyThroughRelation)

	single := many.One()

	leaf, ok := single.(*hasOneThroughRelationQuery)
	if !ok {
		t.Fatalf("One must return the has-one-through leaf, got %T", single)
	}
	if leaf.info.Kind != KindHasOneThrough {
		t.Fatalf("expected KindHasOneThrough, got %d", leaf.info.Kind)
	}
	if single.GetParent() != "parent-model" {
		t.Fatalf("One must keep the parent, got %v", single.GetParent())
	}
	// The through projection carries over: the qualified parent key is the through table's
	// second local key on the through table.
	if got := single.GetQualifiedParentKeyName(); got != "pivots.id" {
		t.Fatalf("qualified parent key = %q, want %q", got, "pivots.id")
	}

	// The narrowed object is a single-result relation: GetResults runs the single-row read.
	var dest struct{}
	if err := single.GetResults(&dest); err != nil {
		t.Fatalf("GetResults: %v", err)
	}
	if firstRows != 1 {
		t.Fatalf("GetResults must read one row through the FirstRow hook, called %d times", firstRows)
	}

	// One() narrows, it never widens: the leaf no longer exposes the HasManyThrough surface.
	if _, ok := single.(contractsorm.HasManyThroughRelation); ok {
		t.Fatal("the narrowed object must not expose One()")
	}
}
