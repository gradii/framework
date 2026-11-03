package relations

import "strings"

// morphOneOrManyRelation carries the surface shared by the MorphOne / MorphMany objects — the Go
// form of Illuminate\Database\Eloquent\Relations\MorphOneOrMany (vendor, illuminate/database
// v12.55.1; MorphOneOrMany.php:16 `abstract class MorphOneOrMany extends HasOneOrMany`). PHP's
// inheritance chain is mirrored by the embedding chain: morphOneOrManyRelation embeds
// hasOneOrManyRelation the way MorphOneOrMany extends HasOneOrMany, and the leaves
// (morphOneRelationQuery / morphManyRelationQuery) embed this type the way MorphOne / MorphMany
// extend it.
//
// Morph fields plumbing: the morph columns ride the plain-data RelationInfo projection through
// the chain — RelationInfo.MorphTypeColumn (the "<name>_type" column as declared; Laravel itself
// stores it related-table-qualified at construction and both accessors below mirror Laravel's
// outputs) and RelationInfo.MorphValue (the parent's morph class value: the declared morph-map
// alias, or the model's table name — the framework's documented alias-or-table convention).
// Morph clause building stays builder-side by design: database/gorm's newMorphOneOrManyQuery
// compiles the morph type + foreign-key constraints, the relation-write shells stamp the morph
// values, and the eager loader groups by morph type — this object only reads the projected
// fields (mirroring MorphOneOrMany::__construct, which stores ($morphType, $parent->getMorphClass())
// before the parent constructor runs).
type morphOneOrManyRelation struct{ hasOneOrManyRelation }

// MorphType reports the morph type column without its table qualifier — the last dot-segment of
// the stored column. Laravel's MorphOneOrMany::getMorphType() (MorphOneOrMany.php:153,
// `last(explode('.', $this->morphType))`; oracle v12.55.1: "commentable_type").
func (m *morphOneOrManyRelation) MorphType() string {
	if index := strings.LastIndex(m.info.MorphTypeColumn, "."); index >= 0 {
		return m.info.MorphTypeColumn[index+1:]
	}

	return m.info.MorphTypeColumn
}

// QualifiedMorphType reports the morph type column qualified with the related table — Laravel's
// MorphOneOrMany::getQualifiedMorphType() (MorphOneOrMany.php:143, returning the column
// HasRelationships::morphOne/morphMany qualified at construction; oracle v12.55.1, executed:
// `$post->morphOne(Image::class, 'imageable')` reports "mp_images.imageable_type"). The Go
// projection stores the column as declared — the default is the bare "<name>_type" — so the
// accessor re-qualifies with the projection's related table to mirror the Laravel output; a
// column that already carries a dot passes through as-is.
func (m *morphOneOrManyRelation) QualifiedMorphType() string {
	if strings.Contains(m.info.MorphTypeColumn, ".") || m.info.RelatedTable == "" {
		return m.info.MorphTypeColumn
	}

	return m.info.RelatedTable + "." + m.info.MorphTypeColumn
}

// MorphClass reports the parent's morph class value — Laravel's MorphOneOrMany::getMorphClass()
// (MorphOneOrMany.php:163; oracle v12.55.1: "ProbePost" for a plain model), which returns the
// class name or a morph-map alias; the Go projection carries the alias-or-table convention into
// RelationInfo.MorphValue.
func (m *morphOneOrManyRelation) MorphClass() string { return m.info.MorphValue }
