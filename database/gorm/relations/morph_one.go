package relations

import contractsorm "github.com/gradii/framework/contracts/database/orm"

// morphOneRelationQuery is the leaf for the KindMorphOne kind — Eloquent's MorphOne (vendor,
// illuminate/database v12.55.1; MorphOne.php:20 `class MorphOne extends MorphOneOrMany implements
// SupportsPartialRelations`, composing the CanBeOneOfMany / ComparesRelatedModels /
// SupportsDefaultModels concerns). The embedding chain mirrors the PHP chain:
// morphOneRelationQuery -> morphOneOrManyRelation -> hasOneOrManyRelation.
//
// The HasOne-shaped read rides the shared core by kind: the single-row read with the WithDefault
// materialisation on a miss (MorphOne::getResults), the one-of-many participation
// (kindSupportsPartialRelations) and the morph-field accessors travel the embedding chain above.
// Morph clause building stays builder-side by design (database/gorm's newMorphOneOrManyQuery
// compiles the morph type + foreign-key constraints; oracle v12.55.1: `select * from
// "probe_comments" where "probe_comments"."commentable_type" = ? and
// "probe_comments"."commentable_id" = ? and "probe_comments"."commentable_id" is not null`).
// Wrap builds this leaf for KindMorphOne.
type morphOneRelationQuery struct{ morphOneOrManyRelation }

// WithDefault overrides the relation's default related instance at call time — MorphOne's
// SupportsDefaultModels::withDefault (vendor v12.55.1, MorphOne.php:22 composes the trait). The
// fresh default instance carries the polymorphic id column from the parent's local key and the
// morph type column from the relation's morph value (MorphOne::newRelatedInstanceFor,
// MorphOne.php:102-110). Oracle (v12.55.1, executed,
// /tmp/lalign-defaults-inverse/runtime_withdefault.php): morphOne.missing-row.attributes
// {"imageable_id":7,"imageable_type":"RtPage"}.
func (m *morphOneRelationQuery) WithDefault(value any) contractsorm.HasOneRelation {
	m.setWithDefault(value)

	return m
}

// WithoutChaperone — see contracts.HasOneOrManyRelation.WithoutChaperone; the leaf-typed form
// keeps the morph accessors reachable after the call.
func (m *morphOneRelationQuery) WithoutChaperone() contractsorm.HasOneOrManyRelation {
	m.clearChaperone()

	return m
}

// WithoutInverse — see contracts.HasOneOrManyRelation.WithoutInverse, the alias.
func (m *morphOneRelationQuery) WithoutInverse() contractsorm.HasOneOrManyRelation {
	m.clearChaperone()

	return m
}

// Compile assertion: the leaf is the declared morph contract's implementation — only the morph
// leaves satisfy the declared morph interfaces (see contracts/database/orm/relation_query.go);
// the shared non-morph leaves narrow through the plain aliases only.
var _ contractsorm.MorphOneRelation = (*morphOneRelationQuery)(nil)
