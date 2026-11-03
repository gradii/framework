package relations

import (
	"testing"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
)

func TestWrap_DispatchesPerKind(t *testing.T) {
	cases := []struct {
		kind  Kind
		check func(contractsorm.RelationQuery) bool
	}{
		{KindHasOne, func(q contractsorm.RelationQuery) bool { _, ok := q.(contractsorm.HasOneRelation); return ok }},
		{KindMorphOne, func(q contractsorm.RelationQuery) bool { _, ok := q.(contractsorm.HasOneRelation); return ok }},
		{KindHasMany, func(q contractsorm.RelationQuery) bool { _, ok := q.(contractsorm.HasManyRelation); return ok }},
		{KindMorphMany, func(q contractsorm.RelationQuery) bool { _, ok := q.(contractsorm.HasManyRelation); return ok }},
		{KindBelongsTo, func(q contractsorm.RelationQuery) bool { _, ok := q.(contractsorm.BelongsToRelation); return ok }},
		{KindMorphTo, func(q contractsorm.RelationQuery) bool { _, ok := q.(contractsorm.BelongsToRelation); return ok }},
		{KindMany2Many, func(q contractsorm.RelationQuery) bool { _, ok := q.(contractsorm.BelongsToManyRelation); return ok }},
		{KindMorphToMany, func(q contractsorm.RelationQuery) bool { _, ok := q.(contractsorm.BelongsToManyRelation); return ok }},
		{KindHasOneThrough, func(q contractsorm.RelationQuery) bool { _, ok := q.(contractsorm.HasOneThroughRelation); return ok }},
		{KindHasManyThrough, func(q contractsorm.RelationQuery) bool { _, ok := q.(contractsorm.HasManyThroughRelation); return ok }},
	}

	for _, tc := range cases {
		rel := Wrap(nil, nil, "relation", RelationInfo{Kind: tc.kind}, Hooks{})
		if !tc.check(rel) {
			t.Fatalf("kind %d: wrapped object does not satisfy its kind interface (%T)", tc.kind, rel)
		}
	}
}

func TestWrapBase_BaseSurfaceOnly(t *testing.T) {
	rel := WrapBase(nil, nil, "relation", Hooks{})

	if _, ok := rel.(contractsorm.HasManyRelation); ok {
		t.Fatal("a base-only relation object must not expose the kind write surface")
	}
	if rel.GetParent() != nil || rel.GetRelated() != nil {
		t.Fatal("a base-only relation object carries no parent/related")
	}
}

func TestRelationQuery_AccessorsAndQualifiedParentKey(t *testing.T) {
	rel := Wrap(nil, "parent-model", "books", RelationInfo{
		Kind:         KindHasOne,
		ParentTable:  "users",
		RelatedTable: "books",
		RelatedModel: "related-model",
		References:   []RefKey{{PrimaryColumn: "id", ForeignColumn: "user_id"}},
	}, Hooks{})

	if rel.GetParent() != "parent-model" {
		t.Fatalf("GetParent = %v", rel.GetParent())
	}
	if rel.GetRelated() != "related-model" {
		t.Fatalf("GetRelated = %v", rel.GetRelated())
	}
	if rel.GetQualifiedParentKeyName() != "users.id" {
		t.Fatalf("GetQualifiedParentKeyName = %q", rel.GetQualifiedParentKeyName())
	}
}

func TestRelationQuery_Is_ComparesKeys(t *testing.T) {
	core := &relationQuery{
		info: RelationInfo{
			Kind:         KindHasOne,
			RelatedTable: "books",
			References:   []RefKey{{PrimaryColumn: "id", ForeignColumn: "user_id"}},
		},
		parent: map[string]any{"id": 7},
		hooks: Hooks{
			ReadColumn:      func(model any, column string) (any, error) { return model.(map[string]any)[column], nil },
			ReadModelColumn: func(model any, column string) (any, error) { return model.(map[string]any)[column], nil },
			ModelTable:      func(model any) string { table, _ := model.(map[string]any)["__table"].(string); return table },
		},
	}

	if !core.Is(map[string]any{"user_id": 7, "__table": "books"}) {
		t.Fatal("same table and equal keys must match")
	}
	if !core.Is(map[string]any{"user_id": uint8(7), "__table": "books"}) {
		t.Fatal("numeric keys compare across Go numeric kinds")
	}
	if !core.Is(map[string]any{"user_id": "7", "__table": "books"}) {
		t.Fatal("numeric strings compare numerically")
	}
	if core.Is(map[string]any{"user_id": 7, "__table": "posts"}) {
		t.Fatal("a different related table must not match")
	}
	if core.Is(map[string]any{"user_id": 8, "__table": "books"}) {
		t.Fatal("a different key must not match")
	}
	if !core.IsNot(map[string]any{"user_id": 8, "__table": "books"}) {
		t.Fatal("IsNot must negate Is")
	}
	if core.Is(nil) {
		t.Fatal("nil never matches")
	}
}

func TestRelationQuery_GetResults_SingleMissMaterialisesDefault(t *testing.T) {
	type defaultModel struct{ Name string }

	core := &relationQuery{
		info: RelationInfo{Kind: KindHasOne},
		hooks: Hooks{
			FirstRow: func(query contractsorm.Query, dest any) (bool, error) { return false, nil },
			BuildDefault: func(value any, parent any) (any, bool, error) {
				return &defaultModel{Name: "fallback"}, true, nil
			},
		},
	}

	var got defaultModel
	if err := core.GetResults(&got); err != nil {
		t.Fatalf("GetResults: %v", err)
	}
	if got.Name != "fallback" {
		t.Fatalf("expected the WithDefault instance on a miss, got %#v", got)
	}
}

func TestHasManyRelation_One_NarrowsTheKind(t *testing.T) {
	many := Wrap(nil, nil, "logins", RelationInfo{Kind: KindHasMany}, Hooks{}).(contractsorm.HasManyRelation)

	leaf, ok := many.One().(*hasOneRelationQuery)
	if !ok {
		t.Fatal("One must return the single-result leaf")
	}
	if leaf.info.Kind != KindHasOne {
		t.Fatalf("expected KindHasOne, got %d", leaf.info.Kind)
	}

	// MorphMany narrows through its own leaf, not the hasMany leaf — morph_many.go:22, the Go
	// analogue of MorphMany::one() (vendor v12.55.1, MorphMany.php:20).
	morphMany := Wrap(nil, nil, "images", RelationInfo{Kind: KindMorphMany}, Hooks{}).(contractsorm.HasManyRelation)
	if _, ok := morphMany.(*morphManyRelationQuery); !ok {
		t.Fatalf("KindMorphMany must wrap the morph leaf, got %T", morphMany)
	}
	morphLeaf, ok := morphMany.One().(*hasOneRelationQuery)
	if !ok {
		t.Fatal("MorphMany One must return the single-result leaf")
	}
	if morphLeaf.info.Kind != KindMorphOne {
		t.Fatalf("expected KindMorphOne, got %d", morphLeaf.info.Kind)
	}
}

func TestRelationQuery_QualifiedParentKeyPerKind(t *testing.T) {
	// Oracle (illuminate/database v12.55.1, reflection): $user->books() -> "p5_users.id",
	// $book->author() -> "p6_books.id" (the child's own qualified key), $stub->imageable() ->
	// "p6_stubs.id" (MorphTo inherits BelongsTo's parent), and
	// $user->car_through_owner() with secondLocalKey "code" -> "p6_owners.code" (the through
	// parent's second local key on the through table).
	belongsTo := Wrap(nil, nil, "author", RelationInfo{
		Kind:                   KindBelongsTo,
		ParentTable:            "books",
		ParentPrimaryKeyColumn: "id",
		RelatedTable:           "authors",
		References:             []RefKey{{PrimaryColumn: "id", ForeignColumn: "author_id"}},
	}, Hooks{})
	if got := belongsTo.GetQualifiedParentKeyName(); got != "books.id" {
		t.Fatalf("belongs-to qualified parent key = %q, want %q", got, "books.id")
	}

	morphTo := Wrap(nil, nil, "imageable", RelationInfo{
		Kind:                   KindMorphTo,
		ParentTable:            "stubs",
		ParentPrimaryKeyColumn: "id",
		MorphIDColumn:          "imageable_id",
		MorphOwnerKey:          "id",
	}, Hooks{})
	if got := morphTo.GetQualifiedParentKeyName(); got != "stubs.id" {
		t.Fatalf("morph-to qualified parent key = %q, want %q", got, "stubs.id")
	}

	through := Wrap(nil, nil, "carThroughOwner", RelationInfo{
		Kind:           KindHasOneThrough,
		ParentTable:    "users",
		ThroughTable:   "owners",
		LocalKey:       "id",
		SecondKey:      "owner_id",
		SecondLocalKey: "code",
	}, Hooks{})
	if got := through.GetQualifiedParentKeyName(); got != "owners.code" {
		t.Fatalf("through qualified parent key = %q, want %q", got, "owners.code")
	}
}
