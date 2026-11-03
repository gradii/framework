package relations

import (
	"strings"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
)

// morphToManyRelationQuery is the leaf for the KindMorphToMany kind — Eloquent's MorphToMany
// (vendor, illuminate/database v12.55.1; MorphToMany.php:18 `class MorphToMany extends
// BelongsToMany`). The embedding chain (morphToManyRelationQuery -> belongsToManyRelationQuery ->
// relationQuery) mirrors the PHP chain. The inverse declaration (morphedByMany; Laravel's
// $inverse constructor flag) is projected as RelationInfo.MorphInverse — there is no
// KindMorphedByMany: Laravel has no MorphedByMany class, the inverse form is the same MorphToMany
// constructed with inverse=true.
//
// The morph pivot filtering (the qualified morph type column on the pivot table — Laravel's
// overridden addWhereConstraints / addEagerConstraints / getRelationExistenceQuery) and the pivot
// write stamps stay builder-side by design (database/gorm's newMany2ManyQuery morph filter and
// the relation-write shells); this object carries the pivot surface through the belongs-to-many
// base. Wrap builds this leaf for KindMorphToMany.
type morphToManyRelationQuery struct{ belongsToManyRelationQuery }

// MorphType reports the pivot's morph type column name, as declared — Laravel's
// MorphToMany::getMorphType() (MorphToMany.php:199, `return $this->morphType;`; oracle v12.55.1,
// executed: "taggable_type" for both morphToMany and morphedByMany).
func (m *morphToManyRelationQuery) MorphType() string { return m.info.MorphTypeColumn }

// QualifiedMorphType reports the morph type column qualified with the pivot table — Laravel's
// MorphToMany::getQualifiedMorphTypeName() (MorphToMany.php:209, the qualifyPivotColumn
// convention: a column already carrying a dot passes through). The projection stores the column
// unqualified, so the accessor adds the projection's pivot table; oracle v12.55.1, executed:
// "taggables.taggable_type" for both morphToMany and morphedByMany.
func (m *morphToManyRelationQuery) QualifiedMorphType() string {
	if strings.Contains(m.info.MorphTypeColumn, ".") || m.info.PivotTable == "" {
		return m.info.MorphTypeColumn
	}

	return m.info.PivotTable + "." + m.info.MorphTypeColumn
}

// MorphClass reports the morph class value the pivot rows pin — Laravel's
// MorphToMany::getMorphClass() (MorphToMany.php:219): the parent's morph class for morphToMany,
// the related's for morphedByMany (both resolved at declaration into RelationInfo.MorphValue;
// oracle v12.55.1, executed: "MpPost" plain and "mp_post" under a morph map, on both forms).
func (m *morphToManyRelationQuery) MorphClass() string { return m.info.MorphValue }

// Inverse reports whether the relation is the morphedByMany (inverse) form — Laravel's
// MorphToMany::getInverse() (MorphToMany.php:229; oracle v12.55.1, executed: false for
// morphToMany, true for morphedByMany).
func (m *morphToManyRelationQuery) Inverse() bool { return m.info.MorphInverse }

// NewPivot overrides the base construction with Laravel's MorphToMany::newPivot (vendor
// v12.55.1, MorphToMany.php:156-172): the morph type/value pair merges into the attributes FIRST
// — `array_merge([$this->morphType => $this->morphClass], $attributes)`, so caller attributes win
// — a Using factory receives the merged map, else the relation builds relations.MorphPivot
// through FromMorphAttributes (Laravel's class default `MorphPivot::fromAttributes`), and the
// finished pivot is stamped with the pivot keys, the related model, the morph type and the morph
// class. Oracle (v12.55.1, executed, /tmp/decide-light-using/using_probe.php): `U2e morph
// newPivot class/exists => [MorphPivot, false]`, `U2e morph newPivot attributes =>
// ['taggable_type' => 'LuPost', 'level' => 'gold']`, `U2e morph newPivot dirty => []`,
// `U2f custom morph newPivot class/exists => [LuTagPivot, false]`. A factory returning a plain
// *Pivot on a morph relation cannot carry the morph save keys (Pivot declares no morph setters):
// it is stamped with the keys and related model only, and the factory should return a
// *MorphPivot for the morph kinds.
func (m *morphToManyRelationQuery) NewPivot(attributes map[string]any, exists bool) (contractsorm.PivotModel, error) {
	merged := make(map[string]any, len(attributes)+1)
	merged[m.info.MorphTypeColumn] = m.info.MorphValue
	for column, value := range attributes {
		merged[column] = value
	}

	if m.pivotFactory != nil {
		pivot, err := m.pivotFactory(m.parent, merged, m.info.PivotTable, exists)
		if err != nil {
			return nil, err
		}

		switch typed := pivot.(type) {
		case *MorphPivot:
			// The morph setters run first: the promoted SetPivotKeys hands back the embedded
			// *Pivot, so the morph half must stay on the *MorphPivot receiver; the concrete type
			// is returned so the morph identity survives.
			typed.SetMorphType(m.info.MorphTypeColumn).SetMorphClass(m.info.MorphValue)
			typed.SetPivotKeys(m.info.PivotParentRef.ForeignColumn, m.info.PivotRelatedRef.ForeignColumn).
				SetRelatedModel(m.info.RelatedModel)

			return typed, nil
		case *Pivot:
			return typed.SetPivotKeys(m.info.PivotParentRef.ForeignColumn, m.info.PivotRelatedRef.ForeignColumn).
				SetRelatedModel(m.info.RelatedModel), nil
		}

		return pivot, nil
	}

	pivot, err := FromMorphAttributes(m.Builder, m.parent, merged, m.info.PivotTable, exists)
	if err != nil {
		return nil, err
	}

	pivot.SetMorphType(m.info.MorphTypeColumn).SetMorphClass(m.info.MorphValue)
	pivot.SetPivotKeys(m.info.PivotParentRef.ForeignColumn, m.info.PivotRelatedRef.ForeignColumn).
		SetRelatedModel(m.info.RelatedModel)

	return pivot, nil
}

// NewExistingPivot is NewPivot with exists true — IWPT::newExistingPivot (:646) over the
// MorphToMany override. The pair is re-declared together because Go embedding has no virtual
// dispatch: a promoted base NewExistingPivot would run the base NewPivot, missing the morph
// merge (the PHP class carries only NewPivot; its NewExistingPivot delegates to it).
func (m *morphToManyRelationQuery) NewExistingPivot(attributes map[string]any) (contractsorm.PivotModel, error) {
	return m.NewPivot(attributes, true)
}

// Using overrides the promoted base method so the chain keeps the morph object — Go method
// promotion returns the embedded base object, which would drop the morph leaf (the NewPivot
// override, MorphType and the morph stamps) from the chain, where PHP's `using()` returns $this,
// the MorphToMany instance. The other fluent methods on this leaf keep the promoted return: the
// pivot-surface methods are exercised through the base contract, while the NewPivot family is
// per-kind and must stay on this object.
func (m *morphToManyRelationQuery) Using(factory contractsorm.PivotFactory) contractsorm.BelongsToManyRelation {
	m.pivotFactory = factory

	return m
}

// Compile assertion: the leaf is the declared morph contract's implementation — only the morph
// leaves satisfy the declared morph interfaces (see contracts/database/orm/relation_query.go);
// the shared non-morph leaves narrow through the plain aliases only.
var _ contractsorm.MorphToManyRelation = (*morphToManyRelationQuery)(nil)
