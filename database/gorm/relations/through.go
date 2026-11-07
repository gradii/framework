package relations

import contractsorm "github.com/gradii/framework/contracts/database/orm"

// hasOneOrManyThroughRelation carries the write surface shared by the HasOneThrough /
// HasManyThrough objects — Eloquent's HasOneOrManyThrough create subset. The through kinds hold
// no key on the related row, so the find-then-write methods carry no foreign key to set. The
// methods whose names overlap Query keep the Query signature (see RelationQuery).
type hasOneOrManyThroughRelation struct{ *relationQuery }

func (t *hasOneOrManyThroughRelation) FirstOrNew(dest any, attributes any, values ...any) error {
	return t.Builder.FirstOrNewRelation(t.parent, t.name, attributeMap(attributes), attributeMapFrom(values...), dest)
}

func (t *hasOneOrManyThroughRelation) FirstOrCreate(dest any, conds ...any) error {
	return t.Builder.FirstOrCreateRelation(t.parent, t.name, attributeMapFrom(conds...), attributeMapAt(conds, 1), dest)
}

func (t *hasOneOrManyThroughRelation) UpdateOrCreate(dest any, attributes any, values any) error {
	return t.Builder.UpdateOrCreateRelation(t.parent, t.name, attributeMap(attributes), attributeMap(values), dest)
}

func (t *hasOneOrManyThroughRelation) CreateOrFirst(dest any, conds ...any) error {
	return t.Builder.CreateOrFirstRelation(t.parent, t.name, attributeMapFrom(conds...), attributeMapAt(conds, 1), dest)
}

// WithDefault overrides the relation's default related instance at call time — HasOneThrough's
// SupportsDefaultModels::withDefault (vendor v12.55.1, HasOneThrough.php:22/24 composes the trait;
// its default instance is a plain newInstance of the related model, newRelatedInstanceFor :108).
// The method rides the shared through base, so a HasManyThrough object carries it too — a Go
// surface superset: Laravel's HasManyThrough has no SupportsDefaultModels, and a many-result read
// never consults the slot (GetResults materialises a default for the single-result kinds only).
// Oracle (v12.55.1, executed, /tmp/lalign-defaults-inverse/runtime_withdefault.php):
// hasOneThrough.missing-row.class "RtCar" with attributes [], hasOneThrough.null-key.queries 0.
func (t *hasOneOrManyThroughRelation) WithDefault(value any) contractsorm.HasOneThroughRelation {
	t.setWithDefault(value)

	return t
}

// WithTrashedParents includes soft-deleted THROUGH rows in the relation — Eloquent's
// HasOneOrManyThrough::withTrashedParents (vendor v12.55.1, HasOneOrManyThrough.php:157,
// `$this->query->withoutGlobalScope('SoftDeletableHasManyThrough'); return $this;`). It appends the
// through-side scope's name to the relation builder's removals, so applyThroughSoftDelete drops
// only the through predicate and the related model's SoftDeletingScope stays in place. The relation
// object is returned, as Laravel's `return $this`; the leaves re-declare it so the concrete
// relation survives, the way Take / Limit do.
//
// Oracle (illuminate/database v12.55.1, real sqlite, /tmp/r12/scope_removal_probe3.php §E/§F2):
// `$country->posts()` compiles `... where "sr_users"."country_id" = ? and "sr_posts"."deleted_at"
// is null and "sr_users"."deleted_at" is null`; `->withTrashedParents()` compiles the same without
// the through predicate (`... and "sr_posts"."deleted_at" is null`), identical to
// `->withoutGlobalScope('SoftDeletableHasManyThrough')`; an eager
// `with(['posts' => fn ($q) => $q->withTrashedParents()])` drops it too; and a through model with
// no soft-delete field has no such scope, so the call is a no-op (§F7/plainPosts).
func (t *hasOneOrManyThroughRelation) WithTrashedParents() contractsorm.HasOneThroughRelation {
	t.rebuild(t.Builder.WithoutGlobalScopes(contractsorm.SoftDeletableHasManyThrough))

	return t
}

// --- take / limit ---------------------------------------------------------------------------
//
// HasOneOrManyThrough::take (vendor v12.55.1, HasOneOrManyThrough.php:761) and
// HasOneOrManyThrough::limit (:772):
//
//	public function limit($value)
//	{
//	    if ($this->farParent->exists) {
//	        $this->query->limit($value);
//	    } else {
//	        $column = $this->getQualifiedFirstKeyName();
//
//	        $grammar = $this->query->getQuery()->getGrammar();
//
//	        if ($grammar instanceof MySqlGrammar && $grammar->useLegacyGroupLimit($this->query->getQuery())) {
//	            $column = 'laravel_through_key';
//	        }
//
//	        $this->query->groupLimit($value, $column);
//	    }
//
//	    return $this;
//	}
//
// An existing far parent keeps the plain query limit; one that does not exist caps the rows per
// far parent through the group limit partitioned by the through table's first key — not the
// related foreign key the has-many family partitions by. Oracle (illuminate/database v12.55.1,
// executed against sqlite, /tmp/followup-take-grouplimit/take_grouplimit.php): T1 existing
// country `select * from "gl_tposts" inner join "gl_tusers" on "gl_tusers"."id" =
// "gl_tposts"."user_id" where "gl_tusers"."country_id" = ? limit 3` (T3 toBase()->limit 3,
// groupLimit NULL); T5 new country `select * from (select *, row_number() over (partition by
// "gl_tusers"."country_id") as "laravel_row" from "gl_tposts" inner join ... where ...) as
// "laravel_table" where "laravel_row" <= 3 order by "laravel_row"` (T4 getQualifiedFirstKeyName
// 'gl_tusers.country_id'; T7 toBase()->groupLimit ['value' => 3, 'column' =>
// 'gl_tusers.country_id']); E2 the eager form wears shouldSelect inside the rewrite and reads one
// post per country. The legacy MySQL branch (MySQL before 8.0.11 numbers rows through session
// variables and partitions by the aliased `laravel_through_key`) is ported from the source lines
// above and read off the seam's UseLegacyGroupLimit; it is pinned on the real MySQL grammar given
// a 5.7.44 server version (tests/database/gorm/dialect_shapes_test.go, oracle
// /tmp/r15bd/legacy.php E2).

// Take is the alias of Limit — HasOneOrManyThrough::take (HasOneOrManyThrough.php:761).
func (t *hasOneOrManyThroughRelation) Take(value int) contractsorm.Query {
	return t.Limit(value)
}

// Limit sets the relation's limit — HasOneOrManyThrough::limit (HasOneOrManyThrough.php:772):
// the plain query limit for an existing far parent, the per-parent group limit on
// getQualifiedFirstKeyName() (laravel_through_key under the legacy MySQL rewrite) otherwise. The
// relation object is returned, as Laravel's `return $this`.
func (t *hasOneOrManyThroughRelation) Limit(value int) contractsorm.Query {
	if t.parentExists() {
		t.rebuild(t.Builder.Limit(value))

		return t
	}

	column := t.qualifiedFirstKeyName()
	if t.Builder.UseLegacyGroupLimit() {
		column = "laravel_through_key"
	}
	t.rebuild(t.Builder.GroupLimit(value, column))

	return t
}

// qualifiedFirstKeyName is the through table's foreign key to the far parent, qualified with the
// through table — HasOneOrManyThrough::getQualifiedFirstKeyName (HasOneOrManyThrough.php:816,
// `return $this->throughParent->qualifyColumn($this->firstKey);`; oracle T4 "gl_tusers.country_id").
func (t *hasOneOrManyThroughRelation) qualifiedFirstKeyName() string {
	return qualifyRelatedColumn(t.info.ThroughTable, t.info.FirstKey)
}

// hasOneThroughRelationQuery is the leaf for the HasOneThrough kind.
type hasOneThroughRelationQuery struct{ hasOneOrManyThroughRelation }

// hasManyThroughRelationQuery is the leaf for the HasManyThrough kind.
type hasManyThroughRelationQuery struct{ hasOneOrManyThroughRelation }

// Take / Limit on the through leaves hand back the leaf — Laravel's `return $this` is the concrete
// relation; a method on the embedded hasOneOrManyThroughRelation can only return the embedded part.

// Take — HasOneOrManyThrough::take on the HasOneThrough leaf.
func (t *hasOneThroughRelationQuery) Take(value int) contractsorm.Query { return t.Limit(value) }

// Limit — HasOneOrManyThrough::limit on the HasOneThrough leaf.
func (t *hasOneThroughRelationQuery) Limit(value int) contractsorm.Query {
	t.hasOneOrManyThroughRelation.Limit(value)

	return t
}

// WithTrashedParents — HasOneOrManyThrough::withTrashedParents on the HasOneThrough leaf; the leaf
// is returned so the object stays the concrete relation (Laravel's `return $this`).
func (t *hasOneThroughRelationQuery) WithTrashedParents() contractsorm.HasOneThroughRelation {
	t.hasOneOrManyThroughRelation.WithTrashedParents()

	return t
}

// Take — HasOneOrManyThrough::take on the HasManyThrough leaf.
func (t *hasManyThroughRelationQuery) Take(value int) contractsorm.Query { return t.Limit(value) }

// Limit — HasOneOrManyThrough::limit on the HasManyThrough leaf.
func (t *hasManyThroughRelationQuery) Limit(value int) contractsorm.Query {
	t.hasOneOrManyThroughRelation.Limit(value)

	return t
}

// WithTrashedParents — HasOneOrManyThrough::withTrashedParents on the HasManyThrough leaf; the leaf
// is returned so the object stays the concrete relation (Laravel's `return $this`).
func (t *hasManyThroughRelationQuery) WithTrashedParents() contractsorm.HasOneThroughRelation {
	t.hasOneOrManyThroughRelation.WithTrashedParents()

	return t
}

// One narrows this many-result object to the HasOneThrough leaf — Laravel's
// HasManyThrough::one() (HasManyThrough.php:25) — reusing the same builder, so every constraint
// the relation carries survives the conversion.
//
// Eloquent implements one() by building a HasOneThrough over the same query with the joins reset
// — tap($this->getQuery(), fn (Builder $query) => $query->getQuery()->joins = []) — inside
// noConstraints(). The new relation's constructor runs addConstraints() with constraints
// disabled, so performJoin re-adds exactly the one join the new relation resolves (the same
// through parent and keys) while the original relation's where (through.firstKey = parent local
// key) survives on the shared query; the observable converted query is the original
// HasManyThrough statement read through the single-result shape. The desc copy below reaches the
// same observable by reusing the builder untouched — the through-read join and constraints
// already sit on it — and flipping only info.Kind, which turns GetResults into the single-row
// read (firstRow, WithDefault on a miss). Oracle-pinned (illuminate/database v12.55.1, executed
// against sqlite, country 1 with two posts):
//
//	$country->posts()->one()->getResults()
//	  -> select "posts".*, "users"."country_id" as "laravel_through_key" from "posts"
//	     inner join "users" on "users"."id" = "posts"."user_id"
//	     where "users"."country_id" = ? limit 1          (the first Post)
//	$country->posts()->one()->first()  -> the same statement and row
//	$country->posts()->one()->get()    -> the statement without limit 1 (a two-row Collection)
//	$country->posts()->getResults()    -> the statement without limit 1 (a two-row Collection)
//
// The narrowed leaf shares the *OrmBuilder the many-result object wraps, so the reads that ride
// the promoted Query path (Get / First / Find / ...) stay byte-identical between the two objects.
//
// Known edge divergence: Eloquent's joins reset also drops joins the caller had added to the
// relation builder before one() — from both the converted relation and the original object's
// shared builder (oracle, illuminate/database v12.55.1: $country->posts()->join('users as extra',
// ...) then $country->posts()->one() renders both toSql() forms with the base through join only).
// The desc copy leaves the builder untouched, so a caller-added join survives the Go conversion;
// no corpus statement pins a join added before one().
func (t *hasManyThroughRelationQuery) One() contractsorm.HasOneThroughRelation {
	core := *t.relationQuery
	core.info.Kind = KindHasOneThrough

	return &hasOneThroughRelationQuery{hasOneOrManyThroughRelation{&core}}
}
