package relations

import (
	"testing"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
)

// Wrap builds the dedicated morph leaves for the morph kinds — the Go form of Laravel's class
// tree (MorphOne / MorphMany / MorphTo / MorphToMany). The interface assertions cannot catch a
// dispatch revert: the morph aliases in contracts/database/orm are Go aliases of the base
// interfaces, so the shared non-morph leaves satisfy them too; only the concrete leaf identity
// below pins the morph split.
func TestWrap_BuildsTheMorphLeaves(t *testing.T) {
	wrap := func(kind Kind) contractsorm.RelationQuery {
		return Wrap(nil, nil, "relation", RelationInfo{Kind: kind}, Hooks{})
	}

	rel := wrap(KindMorphOne)
	if _, ok := rel.(*morphOneRelationQuery); !ok {
		t.Fatalf("KindMorphOne wrapped %T, want *morphOneRelationQuery", rel)
	}
	rel = wrap(KindMorphMany)
	if _, ok := rel.(*morphManyRelationQuery); !ok {
		t.Fatalf("KindMorphMany wrapped %T, want *morphManyRelationQuery", rel)
	}
	rel = wrap(KindMorphTo)
	if _, ok := rel.(*morphToRelationQuery); !ok {
		t.Fatalf("KindMorphTo wrapped %T, want *morphToRelationQuery", rel)
	}
	rel = wrap(KindMorphToMany)
	if _, ok := rel.(*morphToManyRelationQuery); !ok {
		t.Fatalf("KindMorphToMany wrapped %T, want *morphToManyRelationQuery", rel)
	}
}

// The morph-one-or-many chain carries the morph-field accessors — Laravel's
// MorphOneOrMany::getMorphType / getQualifiedMorphType / getMorphClass (MorphOneOrMany.php:143-166;
// oracle illuminate/database v12.55.1, executed: the stored column is the related-table-qualified
// "v_comments.commentable_type", the plain name is its last dot-segment "commentable_type", and
// the morph class names the parent's morph class).
func TestMorphOneOrMany_MorphFieldAccessors(t *testing.T) {
	type morphAccessors interface {
		MorphType() string
		QualifiedMorphType() string
		MorphClass() string
	}

	for _, kind := range []Kind{KindMorphOne, KindMorphMany} {
		rel := Wrap(nil, nil, "comments", RelationInfo{
			Kind:            kind,
			MorphTypeColumn: "comments.commentable_type",
			MorphValue:      "ProbePost",
		}, Hooks{})

		accessors, ok := rel.(morphAccessors)
		if !ok {
			t.Fatalf("kind %d: %T does not carry the morph accessors", kind, rel)
		}
		if got := accessors.MorphType(); got != "commentable_type" {
			t.Fatalf("kind %d: MorphType = %q, want commentable_type", kind, got)
		}
		if got := accessors.QualifiedMorphType(); got != "comments.commentable_type" {
			t.Fatalf("kind %d: QualifiedMorphType = %q, want comments.commentable_type", kind, got)
		}
		if got := accessors.MorphClass(); got != "ProbePost" {
			t.Fatalf("kind %d: MorphClass = %q, want ProbePost", kind, got)
		}
	}
}

// A morph type column without a table qualifier passes through untouched — Laravel's
// last(explode('.', $morphType)) returns the whole string.
func TestMorphOneOrMany_MorphTypeWithoutQualifier(t *testing.T) {
	rel := Wrap(nil, nil, "comments", RelationInfo{
		Kind:            KindMorphMany,
		MorphTypeColumn: "commentable_type",
	}, Hooks{})

	accessors, ok := rel.(interface{ MorphType() string })
	if !ok {
		t.Fatalf("%T does not carry MorphType", rel)
	}
	if got := accessors.MorphType(); got != "commentable_type" {
		t.Fatalf("MorphType = %q, want commentable_type", got)
	}
}
