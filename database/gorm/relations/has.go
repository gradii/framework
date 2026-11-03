package relations

import (
	dbcontract "github.com/gradii/framework/contracts/database/db"
	contractsorm "github.com/gradii/framework/contracts/database/orm"
)

// hasOneOrManyRelation carries the write surface shared by the HasOne / HasMany / MorphOne /
// MorphMany objects — one-line forwards to the builder's relation write shells, which resolve
// the descriptor and run each write on a fresh session themselves. The methods whose names
// overlap Query keep the Query signature (a Go type cannot carry two same-named methods) and
// interpret the attribute arguments as map-shaped values.
type hasOneOrManyRelation struct{ *relationQuery }

// Make builds an unsaved related model into dest: the caller's attributes merged over the query's
// pending attributes (WithAttributes) — the caller's value wins — with the relation's foreign key
// (and the morph type, for MorphOne/MorphMany) stamped from the parent last; nothing is written.
// Laravel's HasOneOrMany::make() through setForeignAttributesForCreate.
func (h *hasOneOrManyRelation) Make(dest any, attributes ...any) error {
	return h.Builder.MakeRelation(h.parent, h.name, h.mergePendingAttributes(attributeMapFrom(attributes...)), dest)
}

// mergePendingAttributes lays the builder's recorded WithAttributes pairs underneath the caller's
// attributes — Laravel's setForeignAttributesForCreate pending loop, which fills only the keys the
// instance does not already carry (the instance's own attributes and the just-set foreign key
// win). The pending map is read through the bound hook at call time, so pairs recorded after the
// relation object was built still merge; the hook's map is never mutated.
func (h *hasOneOrManyRelation) mergePendingAttributes(attributes map[string]any) map[string]any {
	if h.hooks.PendingAttributes == nil {
		return attributes
	}

	pending := h.hooks.PendingAttributes()
	if len(pending) == 0 {
		return attributes
	}

	merged := make(map[string]any, len(pending)+len(attributes))
	for column, value := range pending {
		merged[column] = value
	}
	for column, value := range attributes {
		merged[column] = value
	}

	return merged
}

func (h *hasOneOrManyRelation) MakeMany(records []map[string]any, dests any) error {
	return h.Builder.MakeManyRelation(h.parent, h.name, records, dests)
}

func (h *hasOneOrManyRelation) Save(child any) error {
	return h.Builder.SaveRelation(h.parent, h.name, child)
}

// SaveQuietly is Save with the model events suppressed for the whole write — Laravel's
// HasOneOrMany::saveQuietly (illuminate/database v12.55.1, HasOneOrMany.php:345,
// `Model::withoutEvents(fn () => $this->save($model))`). The foreign key (and morph type)
// stamp and the persist are Save's; the creating / saving / created / saved events never
// fire. Shell: SaveRelationQuietly (database/gorm/relation_writes.go).
func (h *hasOneOrManyRelation) SaveQuietly(child any) error {
	return h.Builder.SaveRelationQuietly(h.parent, h.name, child)
}

// SaveManyQuietly is the slice form of SaveQuietly — HasOneOrMany::saveManyQuietly
// (HasOneOrMany.php:373): the whole slice saves inside one event-suppressed scope.
func (h *hasOneOrManyRelation) SaveManyQuietly(children any) error {
	return h.Builder.SaveManyRelationQuietly(h.parent, h.name, children)
}

func (h *hasOneOrManyRelation) SaveMany(children any) error {
	return h.Builder.SaveManyRelation(h.parent, h.name, children)
}

func (h *hasOneOrManyRelation) Create(dest any) error {
	return h.Builder.CreateRelation(h.parent, h.name, dest)
}

// CreateQuietly is Create with the model events suppressed — HasOneOrMany::createQuietly
// (HasOneOrMany.php:403). The foreign key (and morph type) stamp and the insert are Create's.
func (h *hasOneOrManyRelation) CreateQuietly(dest any) error {
	return h.Builder.CreateRelationQuietly(h.parent, h.name, dest)
}

// ForceCreate persists dest with the foreign key (and morph type) stamped from the parent,
// mirroring HasOneOrMany::forceCreate (HasOneOrMany.php:414) and its morph override
// (MorphOneOrMany.php:78, which additionally stamps the morph type — a stamp the Go Create
// path already applies for the morph kinds). Go has no mass-assignment guard, so Create is
// already the unguarded write Laravel's forceCreate performs and this is a 1:1 alias of
// Create. Shell: CreateRelation.
func (h *hasOneOrManyRelation) ForceCreate(dest any) error {
	return h.Builder.CreateRelation(h.parent, h.name, dest)
}

// ForceCreateQuietly is ForceCreate with the model events suppressed —
// HasOneOrMany::forceCreateQuietly (HasOneOrMany.php:427).
func (h *hasOneOrManyRelation) ForceCreateQuietly(dest any) error {
	return h.Builder.CreateRelationQuietly(h.parent, h.name, dest)
}

func (h *hasOneOrManyRelation) CreateMany(dests any) error {
	return h.Builder.CreateManyRelation(h.parent, h.name, dests)
}

// CreateManyQuietly is the slice form of CreateQuietly — HasOneOrMany::createManyQuietly
// (HasOneOrMany.php:455): the whole slice creates inside one event-suppressed scope.
func (h *hasOneOrManyRelation) CreateManyQuietly(dests any) error {
	return h.Builder.CreateManyRelationQuietly(h.parent, h.name, dests)
}

// ForceCreateMany is the slice form of ForceCreate — HasOneOrMany::forceCreateMany
// (HasOneOrMany.php:466).
func (h *hasOneOrManyRelation) ForceCreateMany(dests any) error {
	return h.Builder.CreateManyRelation(h.parent, h.name, dests)
}

// ForceCreateManyQuietly is the slice form of ForceCreateQuietly —
// HasOneOrMany::forceCreateManyQuietly (HasOneOrMany.php:483).
func (h *hasOneOrManyRelation) ForceCreateManyQuietly(dests any) error {
	return h.Builder.CreateManyRelationQuietly(h.parent, h.name, dests)
}

func (h *hasOneOrManyRelation) FindOrNew(id any, dest any) error {
	return h.Builder.FindOrNewRelation(h.parent, h.name, id, dest)
}

func (h *hasOneOrManyRelation) FirstOrNew(dest any, attributes any, values ...any) error {
	return h.Builder.FirstOrNewRelation(h.parent, h.name, attributeMap(attributes), attributeMapFrom(values...), dest)
}

func (h *hasOneOrManyRelation) FirstOrCreate(dest any, conds ...any) error {
	return h.Builder.FirstOrCreateRelation(h.parent, h.name, attributeMapFrom(conds...), attributeMapAt(conds, 1), dest)
}

func (h *hasOneOrManyRelation) UpdateOrCreate(dest any, attributes any, values any) error {
	return h.Builder.UpdateOrCreateRelation(h.parent, h.name, attributeMap(attributes), attributeMap(values), dest)
}

func (h *hasOneOrManyRelation) CreateOrFirst(dest any, conds ...any) error {
	return h.Builder.CreateOrFirstRelation(h.parent, h.name, attributeMapFrom(conds...), attributeMapAt(conds, 1), dest)
}

func (h *hasOneOrManyRelation) Upsert(values any, uniqueBy []string, update ...any) (*dbcontract.Result, error) {
	return h.Builder.UpsertRelation(h.parent, h.name, upsertRows(values), uniqueBy, update...)
}

// --- the HasOneOrMany key accessors ------------------------------------------
//
// The four key accessors Eloquent's HasOneOrMany carries (vendor, illuminate/database v12.55.1,
// HasOneOrMany.php:571-628). The keys ride the plain-data projection: References[0] carries the
// parent local key (PrimaryColumn) and the related foreign key (ForeignColumn, stored unqualified
// — the read builders qualify it, database/gorm's newHasOneOrManyQuery), and the declaration
// qualified the foreign key with the related table (Concerns/HasRelationships.php:220/543/314/641
// — `$instance->qualifyColumn($foreignKey)`). The morph leaves inherit the family through the
// embedding chain (MorphOneOrMany extends HasOneOrMany). Oracle (v12.55.1, executed,
// /tmp/lalign-probes/accessors.php): $user->posts() reports "user_id" / "lax_posts.lax_user_id" /
// "id" / "lax_posts.lax_user_id"; the morphMany sibling reports "commentable_id" /
// "lax_comments.commentable_id" / "id" / "lax_comments.commentable_id".

// GetExistenceCompareKey gets the key the existence query compares the parent key against —
// HasOneOrMany::getExistenceCompareKey (HasOneOrMany.php:571,
// `return $this->getQualifiedForeignKeyName();`). The value the relation existence compile sites
// build inline today (database/gorm/queries_relationships.go).
func (h *hasOneOrManyRelation) GetExistenceCompareKey() string {
	return h.GetQualifiedForeignKeyName()
}

// GetForeignKeyName gets the plain (unqualified) foreign key — HasOneOrMany::getForeignKeyName
// (HasOneOrMany.php:601, `array_last(explode('.', $this->getQualifiedForeignKeyName()))`).
func (h *hasOneOrManyRelation) GetForeignKeyName() string {
	if len(h.info.References) == 0 {
		return ""
	}

	return lastColumnSegment(h.info.References[0].ForeignColumn)
}

// GetQualifiedForeignKeyName gets the foreign key qualified with the related table —
// HasOneOrMany::getQualifiedForeignKeyName (HasOneOrMany.php:613, `return $this->foreignKey;` —
// the column the declaration stored qualified). A projection without a reference pair reports "".
func (h *hasOneOrManyRelation) GetQualifiedForeignKeyName() string {
	if len(h.info.References) == 0 {
		return ""
	}

	return qualifyRelatedColumn(h.info.RelatedTable, h.info.References[0].ForeignColumn)
}

// GetLocalKeyName gets the parent's local key — HasOneOrMany::getLocalKeyName
// (HasOneOrMany.php:623, `return $this->localKey;`). A projection without a reference pair
// reports "".
func (h *hasOneOrManyRelation) GetLocalKeyName() string {
	if len(h.info.References) == 0 {
		return ""
	}

	return h.info.References[0].PrimaryColumn
}

// --- take / limit ---------------------------------------------------------------------------
//
// HasOneOrMany::take (vendor v12.55.1, HasOneOrMany.php:544) and HasOneOrMany::limit (:555):
//
//	public function limit($value)
//	{
//	    if ($this->parent->exists) {
//	        $this->query->limit($value);
//	    } else {
//	        $this->query->groupLimit($value, $this->getExistenceCompareKey());
//	    }
//
//	    return $this;
//	}
//
// An existing parent keeps the plain query limit. A parent that does not exist — the prototype an
// eager-load constraint runs the relation on — caps the rows per parent instead, through the query
// builder's group limit: the row_number() over (partition by <existence compare key>) derived
// table, so with(['posts' => fn ($q) => $q->limit(3)]) reads three posts for every user. Oracle
// (illuminate/database v12.55.1, executed against sqlite,
// /tmp/followup-take-grouplimit/take_grouplimit.php): H1 existing user `select * from "gl_posts"
// where "gl_posts"."user_id" = ? and "gl_posts"."user_id" is not null limit 3` (H4 toBase()->limit
// 3, groupLimit NULL); H7 new user `select * from (select *, row_number() over (partition by
// "gl_posts"."user_id") as "laravel_row" from "gl_posts" where ...) as "laravel_table" where
// "laravel_row" <= 3 order by "laravel_row"` (H9 toBase()->limit NULL, groupLimit ['value' => 3,
// 'column' => 'gl_posts.user_id']); H3 the relation itself is returned; H14 HasOne composes the
// same branch; E1 the eager form reads two posts per user. parentExists is the Go reading of
// $this->parent->exists (relationQuery.parentExists).

// Take is the alias of Limit — HasOneOrMany::take (HasOneOrMany.php:544).
func (h *hasOneOrManyRelation) Take(value int) contractsorm.Query {
	return h.Limit(value)
}

// Limit sets the relation's limit — HasOneOrMany::limit (HasOneOrMany.php:555): the plain query
// limit for an existing parent, the per-parent group limit on getExistenceCompareKey() otherwise.
// The relation object is returned, as Laravel's `return $this`.
func (h *hasOneOrManyRelation) Limit(value int) contractsorm.Query {
	if h.parentExists() {
		h.rebuild(h.Builder.Limit(value))
	} else {
		h.rebuild(h.Builder.GroupLimit(value, h.GetExistenceCompareKey()))
	}

	return h
}

// Take / Limit on the leaves hand back the leaf. Laravel's `return $this` is the concrete
// relation (oracle H3 / M5 / M6: Relations\HasMany, Relations\MorphMany, Relations\MorphOne), but a
// method declared on the embedded hasOneOrManyRelation can only return the embedded part, so each
// leaf re-declares the pair over the shared branch above. The morph leaves sit here too: Laravel
// declares no take/limit on MorphOne / MorphMany — they inherit HasOneOrMany's (M2 / M4 compile the
// same rewrite over the morph foreign key) — so HasOneOrMany's home is theirs as well.

// Take — HasOneOrMany::take on the HasOne leaf.
func (h *hasOneRelationQuery) Take(value int) contractsorm.Query { return h.Limit(value) }

// Limit — HasOneOrMany::limit on the HasOne leaf.
func (h *hasOneRelationQuery) Limit(value int) contractsorm.Query {
	h.hasOneOrManyRelation.Limit(value)

	return h
}

// Take — HasOneOrMany::take on the HasMany leaf.
func (h *hasManyRelationQuery) Take(value int) contractsorm.Query { return h.Limit(value) }

// Limit — HasOneOrMany::limit on the HasMany leaf.
func (h *hasManyRelationQuery) Limit(value int) contractsorm.Query {
	h.hasOneOrManyRelation.Limit(value)

	return h
}

// Take — HasOneOrMany::take inherited by the MorphOne leaf.
func (m *morphOneRelationQuery) Take(value int) contractsorm.Query { return m.Limit(value) }

// Limit — HasOneOrMany::limit inherited by the MorphOne leaf.
func (m *morphOneRelationQuery) Limit(value int) contractsorm.Query {
	m.hasOneOrManyRelation.Limit(value)

	return m
}

// Take — HasOneOrMany::take inherited by the MorphMany leaf.
func (m *morphManyRelationQuery) Take(value int) contractsorm.Query { return m.Limit(value) }

// Limit — HasOneOrMany::limit inherited by the MorphMany leaf.
func (m *morphManyRelationQuery) Limit(value int) contractsorm.Query {
	m.hasOneOrManyRelation.Limit(value)

	return m
}

// --- SupportsInverseRelations ----------------------------------------------
//
// The three runtime accessors Eloquent's HasOneOrMany carries through the SupportsInverseRelations
// trait (vendor, illuminate/database v12.55.1, Relations/Concerns/SupportsInverseRelations.php):
// getInverseRelationship (:129), withoutInverse (:141) and withoutChaperone (:151). The Go
// declaration is the relation descriptor's Inverse field — projected as RelationInfo.Inverse and
// installed by database/gorm as the wrapped builder's after-query write-back state. Clearing goes
// through the QueryWithInverseChaperone capability the builder implements, so a subsequent read
// links nothing and a create / make leaves the child's inverse relation unset — exactly the probed
// v12.55.1 behavior (/tmp/lalign-defaults-inverse/inverse_writeback_runtime.php: 8
// gir.after-withoutChaperone NULL, 9 withoutInverse-get.any-relationLoaded false, 6/7
// withoutChaperone-create/make.relationLoaded false).

// GetInverseRelationship — see contracts.HasOneOrManyRelation.GetInverseRelationship.
func (h *hasOneOrManyRelation) GetInverseRelationship() string {
	if carrier, ok := h.Builder.(contractsorm.QueryWithInverseChaperone); ok {
		if name := carrier.InverseRelationship(); name != "" {
			return name
		}
	}

	return h.info.Inverse
}

// clearChaperone clears the object's inverse state: the projection's declared name and the wrapped
// builder's after-query write-back (QueryWithInverseChaperone). The shared body of the per-leaf
// WithoutChaperone / WithoutInverse pair.
func (h *hasOneOrManyRelation) clearChaperone() {
	h.info.Inverse = ""
	if carrier, ok := h.Builder.(contractsorm.QueryWithInverseChaperone); ok {
		carrier.WithoutInverseChaperone()
	}
}

// WithoutChaperone — see contracts.HasOneOrManyRelation.WithoutChaperone. Defined per leaf so the
// returned object is the relation itself — Laravel's `return $this` — keeping the leaf's own
// methods (One, the morph accessors) reachable through a re-narrowing assertion.
func (h *hasOneRelationQuery) WithoutChaperone() contractsorm.HasOneOrManyRelation {
	h.clearChaperone()

	return h
}

// WithoutInverse — see contracts.HasOneOrManyRelation.WithoutInverse, the alias.
func (h *hasOneRelationQuery) WithoutInverse() contractsorm.HasOneOrManyRelation {
	h.clearChaperone()

	return h
}

// WithoutChaperone — see contracts.HasOneOrManyRelation.WithoutChaperone (the per-leaf note above).
func (h *hasManyRelationQuery) WithoutChaperone() contractsorm.HasOneOrManyRelation {
	h.clearChaperone()

	return h
}

// WithoutInverse — see contracts.HasOneOrManyRelation.WithoutInverse, the alias.
func (h *hasManyRelationQuery) WithoutInverse() contractsorm.HasOneOrManyRelation {
	h.clearChaperone()

	return h
}

// hasOneRelationQuery is the leaf for the HasOne / MorphOne kinds.
type hasOneRelationQuery struct{ hasOneOrManyRelation }

// WithDefault overrides the relation's default related instance at call time — HasOne's
// SupportsDefaultModels::withDefault (vendor v12.55.1, Relations/Concerns/SupportsDefaultModels
// .php:32, `$this->withDefault = $callback; return $this;`; HasOne.php:22 composes the trait).
// The call replaces the one slot the declaration seeded (RelationInfo.WithDefault) and the default
// materialises through GetResults on a miss — or on a null parent key with no query at all; the
// fresh instance carries the foreign key stamp (HasOne::newRelatedInstanceFor, HasOne.php:100-106).
// Oracle (v12.55.1, executed, /tmp/lalign-defaults-inverse/runtime_withdefault.php):
// hasOne.missing-row.attributes {"foreign_key":2}, hasOne.null-key.queries 0,
// hasOne.declared-then-runtime.name "runtime".
func (h *hasOneRelationQuery) WithDefault(value any) contractsorm.HasOneRelation {
	h.setWithDefault(value)

	return h
}

// hasManyRelationQuery is the leaf for the HasMany kind; One() narrows it to the single-result
// leaf, reusing the same builder so the relation's constraints survive — Laravel's HasMany::one()
// (vendor v12.55.1, HasMany.php:20, a HasOne over the same query under noConstraints). MorphMany
// narrows through its own morph leaf instead, whose One (morph_many.go:22) mirrors
// MorphMany::one() (MorphMany.php:20) with the KindMorphOne projection.
type hasManyRelationQuery struct{ hasOneOrManyRelation }

func (h *hasManyRelationQuery) One() contractsorm.HasOneRelation {
	core := *h.relationQuery
	core.info.Kind = KindHasOne

	return &hasOneRelationQuery{hasOneOrManyRelation{&core}}
}
