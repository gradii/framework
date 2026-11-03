package relations

import contractsorm "github.com/gradii/framework/contracts/database/orm"

// morphManyRelationQuery is the leaf for the KindMorphMany kind — Eloquent's MorphMany (vendor,
// illuminate/database v12.55.1; MorphMany.php:13 `class MorphMany extends MorphOneOrMany`). The
// embedding chain (morphManyRelationQuery -> morphOneOrManyRelation -> hasOneOrManyRelation)
// mirrors the PHP chain. The many-result read (the collection getResults), the empty-collection
// initRelation and the morph-type-filtered constraints ride the shared core by kind; the morph
// clause building stays builder-side by design (database/gorm's newMorphOneOrManyQuery). Wrap
// builds this leaf for KindMorphMany.
type morphManyRelationQuery struct{ morphOneOrManyRelation }

// One narrows this many-result object to the single-result leaf — Laravel's MorphMany::one()
// (MorphMany.php:20-42), which builds a MorphOne under noConstraints over the same query with the
// relation's ($morphType, $foreignKey, $localKey) and propagates the inverse. The Go form reuses
// the same builder untouched — the relation's constraints already sit on it (oracle v12.55.1:
// $post->comments()->one()->toSql() renders the same morph statement as $post->comments()->toSql())
// — and flips the projected kind to KindMorphOne. The narrowed object keeps the shared hasOne
// leaf (hasOneRelationQuery), the dynamic shape this call has always returned, so every
// constraint and hook survives the conversion.
func (m *morphManyRelationQuery) One() contractsorm.HasOneRelation {
	core := *m.relationQuery
	core.info.Kind = KindMorphOne

	return &hasOneRelationQuery{hasOneOrManyRelation{&core}}
}

// WithoutChaperone — see contracts.HasOneOrManyRelation.WithoutChaperone; the leaf-typed form
// keeps the morph accessors reachable after the call.
func (m *morphManyRelationQuery) WithoutChaperone() contractsorm.HasOneOrManyRelation {
	m.clearChaperone()

	return m
}

// WithoutInverse — see contracts.HasOneOrManyRelation.WithoutInverse, the alias.
func (m *morphManyRelationQuery) WithoutInverse() contractsorm.HasOneOrManyRelation {
	m.clearChaperone()

	return m
}

// Compile assertion: the leaf is the declared morph contract's implementation — only the morph
// leaves satisfy the declared morph interfaces (see contracts/database/orm/relation_query.go);
// the shared non-morph leaves narrow through the plain aliases only.
var _ contractsorm.MorphManyRelation = (*morphManyRelationQuery)(nil)
