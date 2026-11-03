package relations

import contractsorm "github.com/gradii/framework/contracts/database/orm"

// morphToRelationQuery is the leaf for the KindMorphTo kind — Eloquent's MorphTo (vendor,
// illuminate/database v12.55.1; MorphTo.php:18 `class MorphTo extends BelongsTo`). The embedding
// chain (morphToRelationQuery -> belongsToRelationQuery -> relationQuery) mirrors the PHP chain
// (MorphTo -> BelongsTo).
//
// MorphTo overrides only the read internals (the per-row class resolution) and the write
// attribute effects; here the read resolution and the morph clause building stay builder-side by
// design — database/gorm's newMorphToQuery resolves the related class per parent row from the
// morph type and compiles the statement (oracle v12.55.1, type "ProbePost": `select * from
// "probe_posts" where "probe_posts"."id" = ?`), and the eager dictionary lives in eager_loader.go's
// loadMorphTo. The writes (Associate / Dissociate / Disassociate) forward through the belongs-to
// base to the morph-aware write shells, which stamp the morph id column plus the morph type
// column (see belongs_to.go). The morph columns ride RelationInfo (MorphTypeColumn /
// MorphIDColumn / MorphOwnerKey). Wrap builds this leaf for KindMorphTo.
//
// Surface notes (item morphport-morph-to-surface):
//
// The trashed-scope family. Laravel buffers withTrashed / withoutTrashed / onlyTrashed into
// MorphTo::$macroBuffer and replays them on each per-type query (MorphTo.php:366/383/400,
// replayMacros 418-424); each also applies immediately through when(true, callback). The Go engine consumes the
// family as declaration metadata — contractsorm.MorphTo.WithTrashed / WithoutTrashed / OnlyTrashed
// — applied by database/gorm to the per-bucket eager queries (loadMorphTo) and to the object-path
// read (newMorphToQuery). The object needs no declared methods for WithTrashed / OnlyTrashed: the
// promoted Query methods mutate the wrapped builder in place (OrmBuilder.setConditions mutates its
// receiver), which is the immediate half for the instance form (oracle v12.55.1, executed,
// /tmp/morphport-probes/trashed.php: the default read compiles `... and "mpt_posts"."deleted_at"
// is null`; `$comment->commentable()->withTrashed()->first()` drops it and resolves a soft-deleted
// target; `->onlyTrashed()` compiles `is not null`). WithoutTrashed is a recorded residue: the Go
// default read already is withoutTrashed, and clearing a withTrashed / onlyTrashed flag previously
// applied on one builder has no entry point in the frozen Query surface (the declaration's
// WithoutTrashed field covers the eager-side exclusion the Go eager default does not apply).
//
// GetDictionary(). Recorded residue: the Go eager dictionary is loader-private by design —
// loadMorphTo buckets parents into local maps and keeps no relation-object state to expose
// (oracle v12.55.1, executed, /tmp/morphport-probes/dictionary.php: getDictionary() =>
// ['MpdPost' => [5 => [0 => comment, 1 => comment]], 'MpdVideo' => [7 => [0 => comment]]], the
// associative collection's keys preserved). The corpus skips at
// morph_to/laravel_port_morph_to_test.go:259/263 carry the same adjudication.
//
// morphWith / morphWithCount / constrain (MorphTo.php:322/337/352) are declaration-borne
// (contractsorm.MorphTo.MorphWith / MorphWithCount / Constrain, keyed by morph-map alias) and
// consumed per bucket by loadMorphTo; the PHP instance-method chain has no Go object counterpart
// because the Go eager engine reads static declarations instead of re-invoking the relation method
// (the HasOne.Inverse convention), and PHP's __call macro buffering has no consumer on this object.
type morphToRelationQuery struct{ belongsToRelationQuery }

// MorphType reports the morph type column this table carries, as declared — Laravel's
// MorphTo::getMorphType() (MorphTo.php:301, `return $this->morphType;`; MorphTo stores the column
// unqualified, unlike MorphOneOrMany; oracle v12.55.1, executed: morphTo()->getMorphType() is
// "commentable_type").
func (m *morphToRelationQuery) MorphType() string { return m.info.MorphTypeColumn }

// MorphOwnerKey reports the owner key column on each resolved related table — Eloquent's MorphTo
// owner key, read through BelongsTo::getOwnerKeyName (MorphTo.php:431-437, whose null owner key
// reports an empty qualified name). Laravel resolves it per related class as
// $ownerKey ?? $instance->getKeyName() (oracle v12.55.1, executed: the eager form with no type
// set reports NULL, the instance form — type column set — reports "id", an explicit
// morphTo(..., 'uuid') owner key reports "uuid"); the Go descriptor pins the same default at
// build time (descriptorFromMorphTo defaults "id") and the accessor keeps the "id" fallback for
// a zero projection.
func (m *morphToRelationQuery) MorphOwnerKey() string {
	if m.info.MorphOwnerKey == "" {
		return "id"
	}

	return m.info.MorphOwnerKey
}

// Compile assertion: the leaf is the declared morph contract's implementation — only the morph
// leaves satisfy the declared morph interfaces (see contracts/database/orm/relation_query.go);
// the shared non-morph leaves narrow through the plain aliases only.
var _ contractsorm.MorphToRelation = (*morphToRelationQuery)(nil)
