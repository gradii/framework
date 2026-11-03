package relations

import contractsorm "github.com/gradii/framework/contracts/database/orm"

// belongsToRelationQuery is the leaf for the BelongsTo / MorphTo kinds — Eloquent's BelongsTo on
// the relation object. Laravel's MorphTo extends BelongsTo (overriding only the read internals
// and the write attribute effects), so both kinds share this leaf: the writes forward to the
// relation write shells, which re-resolve the descriptor per call and run the morph-aware
// variant for MorphTo — the morph id column plus the morph type column, which takes the owner's
// MorphClass()/morph-map alias or, unaliased, its table name (Laravel's MorphTo::associate
// writes getMorphClass() instead; the alias-or-table fallback is the framework's documented
// convention).
type belongsToRelationQuery struct{ *relationQuery }

func (b *belongsToRelationQuery) Associate(owner any) error {
	return b.Builder.AssociateRelation(b.parent, b.name, owner)
}

func (b *belongsToRelationQuery) Dissociate() error {
	return b.Builder.DissociateRelation(b.parent, b.name)
}

// Disassociate is the MorphTo spelling of Dissociate — Eloquent's MorphTo::disassociate.
func (b *belongsToRelationQuery) Disassociate() error {
	return b.Builder.DissociateRelation(b.parent, b.name)
}

// GetChild gets the child model that holds the foreign key: the parent the relation was resolved
// from — Eloquent's BelongsTo::getChild() ($this->child, the inverse of the relation's "parent"
// naming). MorphTo inherits the accessor unchanged, so a morph-to relation reports the same
// parent-side model.
func (b *belongsToRelationQuery) GetChild() any { return b.parent }

// --- the BelongsTo / MorphTo key accessors -----------------------------------
//
// The five key accessors Eloquent's BelongsTo carries (vendor, illuminate/database v12.55.1,
// BelongsTo.php:306-383); MorphTo extends BelongsTo, inheriting four unchanged and overriding
// getQualifiedOwnerKeyName (MorphTo.php:429-437, the null-owner-key branch). The keys ride the
// projection: BelongsTo's References[0] carries the owner key (PrimaryColumn) and the child's
// foreign key (ForeignColumn); MorphTo carries MorphIDColumn / MorphOwnerKey instead — its
// descriptor declares no reference pair (descriptorFromMorphTo, descriptor.go). The
// child side of the qualified foreign key is the parent model the relation was resolved from
// (Laravel's $this->child — the inverse of the relation's "parent" naming). Oracle (v12.55.1,
// executed, /tmp/lalign-probes/accessors.php): $post->user() reports "lax_user_id" /
// "lax_posts.lax_user_id" / "id" / "lax_users.id" / "user"; $comment->commentable() reports
// "commentable_id" / "lax_comments.commentable_id" / "id" / "lax_users.id" / "commentable"; the
// morphTo eager form (no type set) reports a null owner key (getOwnerKeyName) and "" for the
// qualified name; an explicit morphTo(..., 'custom_key') reports "custom_key".

// GetForeignKeyName gets the plain foreign key the child holds — BelongsTo::getForeignKeyName
// (BelongsTo.php:306, `return $this->foreignKey;`). The MorphTo form reports the morph id column
// — the column MorphTo passes through as $foreignKey (Concerns/HasRelationships.php:401-417,
// morphTo).
func (b *belongsToRelationQuery) GetForeignKeyName() string {
	if b.info.Kind == KindMorphTo {
		return b.info.MorphIDColumn
	}
	if len(b.info.References) > 0 {
		return b.info.References[0].ForeignColumn
	}

	return ""
}

// GetQualifiedForeignKeyName gets the foreign key qualified with the child's own table —
// BelongsTo::getQualifiedForeignKeyName (BelongsTo.php:316, `$this->child->qualifyColumn(...)`).
func (b *belongsToRelationQuery) GetQualifiedForeignKeyName() string {
	return qualifyRelatedColumn(b.info.ParentTable, b.GetForeignKeyName())
}

// GetOwnerKeyName gets the related model's owner key — BelongsTo::getOwnerKeyName
// (BelongsTo.php:336, `return $this->ownerKey;`; MorphTo inherits it unchanged). The MorphTo
// descriptor pins the instance path's resolution (`$ownerKey ?? $instance->getKeyName()`,
// Concerns/HasRelationships.php:446-453) at build time — descriptorFromMorphTo defaults "id" —
// so a resolved descriptor reports exactly the oracle's instance form ("id", "custom_key" for an
// explicit morphTo(..., 'custom_key')); a projection that carries no owner key reports "" (the
// oracle's eager-form null). MorphToRelation.MorphOwnerKey reports the same column through the
// read-path default.
func (b *belongsToRelationQuery) GetOwnerKeyName() string {
	if b.info.Kind == KindMorphTo {
		return b.info.MorphOwnerKey
	}
	if len(b.info.References) > 0 {
		return b.info.References[0].PrimaryColumn
	}

	return ""
}

// GetQualifiedOwnerKeyName gets the owner key qualified with the related table —
// BelongsTo::getQualifiedOwnerKeyName (BelongsTo.php:346), overridden by MorphTo
// (MorphTo.php:429-437) to report "" when the owner key is null (the oracle's eager form; the
// resolved instance form reports the related table qualifying the key).
func (b *belongsToRelationQuery) GetQualifiedOwnerKeyName() string {
	if b.info.Kind == KindMorphTo && b.info.MorphOwnerKey == "" {
		return ""
	}

	return qualifyRelatedColumn(b.info.RelatedTable, b.GetOwnerKeyName())
}

// GetRelationName gets the declared relation name — BelongsTo::getRelationName (BelongsTo.php:380,
// `return $this->relationName;`; MorphTo passes the name through unchanged). The Go form reports
// the name the relation was resolved under, the declared map key.
func (b *belongsToRelationQuery) GetRelationName() string { return b.name }

// WithDefault overrides the relation's default related instance at call time — BelongsTo's
// SupportsDefaultModels::withDefault (vendor v12.55.1, BelongsTo.php:8 composes the trait). The
// default instance is a plain newInstance of the related model with no attributes set
// (BelongsTo::newRelatedInstanceFor, BelongsTo.php:286) and stands in on a missing row and on a
// null foreign key (getResults, :78-85). MorphTo inherits the method through the embedding chain.
// Oracle (v12.55.1, executed, /tmp/lalign-defaults-inverse/runtime_withdefault.php):
// belongsTo.missing-row.attributes [] and belongsTo.null-fk.attributes [].
func (b *belongsToRelationQuery) WithDefault(value any) contractsorm.BelongsToRelation {
	b.setWithDefault(value)

	return b
}
