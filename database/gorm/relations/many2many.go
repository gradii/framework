package relations

import (
	"slices"
	"strings"

	dbcontract "github.com/gradii/framework/contracts/database/db"
	contractsorm "github.com/gradii/framework/contracts/database/orm"
	querybuilder "github.com/gradii/framework/database/query"
)

// belongsToManyRelationQuery is the leaf for the Many2Many / MorphToMany / MorphedByMany kinds —
// Eloquent's BelongsToMany on the relation object: the pivot write family, the pivot-column query
// family and AllRelatedIds. The methods whose names overlap Query keep the Query signature (see
// RelationQuery). The find-or-create family BelongsToMany exposes — FindOrNew / FirstOrNew /
// FirstOrCreate / CreateOrFirst — forwards into the relation write shells, which carry Laravel's
// three-level search, its pivot attach and its write-PDO fallback (see each method below).
type belongsToManyRelationQuery struct {
	*relationQuery

	// pivotColumns is the explicit pivot-column list WithPivot accumulates — the Go form of
	// InteractsWithPivotTable::$pivotColumns (raw list: duplicates and call order kept; the read's
	// SELECT render deduplicates like Laravel's ->unique()). applyPivotRead pushes it to the
	// wrapped builder's pivot read.
	pivotColumns []string
	// pivotValues is the recorded "pivot column = value" list WithPivotValue accumulates — the
	// Go form of BelongsToMany::$pivotValues (BelongsToMany.php:110; entries are
	// ['column' => ..., 'value' => ...], call order and duplicates kept). Base NewPivot folds the
	// list into the attribute bag the way IWPT::newPivot's array_column merge does (:629); the
	// MorphToMany override does not merge it there. The attach / attachOrFail / sync / toggle
	// shells receive the list too and stamp it onto every pivot INSERT row through
	// baseAttachRecord's values loop (:454-472) — see WithPivotValue.
	pivotValues []PivotValue
	// pivotAccessor is the call-time As accessor; "" keeps the declaration's field
	// (RelationInfo.PivotField).
	pivotAccessor string
	// pivotFactory is the call-time Using state — the Go stand-in for BelongsToMany::$using
	// (BelongsToMany.php:138), the custom pivot class the NewPivot family dispatches to. nil keeps
	// the default constructors.
	pivotFactory contractsorm.PivotFactory
}

// PivotValue is one recorded "pivot column = value" pair — the Go form of a BelongsToMany::
// entry (['column' => ..., 'value' => ...]). The column is stored as the call supplied it
// (Laravel records the raw argument, Expression included); only string columns can become
// attribute keys in the NewPivot merge or the pivot INSERT. It aliases the contract-level pair
// (contractsorm.PivotValue) so a relation DECLARATION can carry the same list WithPivotValue
// records at call time — Many2Many.PivotValues / MorphToMany.PivotValues /
// MorphedByMany.PivotValues is Laravel's BelongsToMany::$pivotValues (BelongsToMany.php:110),
// which the create / attach writes stamp onto every pivot row and newPivot merges into its
// attribute bag.
type PivotValue = contractsorm.PivotValue

// FindOrNew returns the attached related row with primary key id, read through the pivot-joined
// relation read and hydrating the related model's Pivot field — Laravel BelongsToMany::findOrNew
// (vendor v12.55.1, BelongsToMany.php:621) delegates to BelongsToMany::find (:729), so an
// unattached row does not match; on a miss Laravel returns $this->related->newInstance(), an
// empty related instance (no foreign attributes to pre-set, no pivot write), whose port form is
// dest left as the caller's new instance. Shell: FindOrNewRelation's Many2Many branch
// (database/gorm/relation_writes.go).
func (m *belongsToManyRelationQuery) FindOrNew(id any, dest any) error {
	return m.Builder.FindOrNewRelation(m.parent, m.name, id, dest)
}

// FindSole returns the attached related row with primary key id, erroring when no row matches and
// when several do — Laravel BelongsToMany::findSole (vendor v12.55.1, BelongsToMany.php:750-757,
// `$this->where($this->getRelated()->getQualifiedKeyName(), '=', $this->parseId($id))->sole($columns)`).
// The Go composition is WhereKey(id)->Sole(dest): WhereKey renders the same related-key equality
// (`where "lax_roles"."id" = ?`) and Sole is the same two-row read Laravel's sole() runs. Oracle
// (v12.55.1, executed, /tmp/lalign-probes/accessors.php): the statement carries the pivot aliases
// and `... and "lax_roles"."id" = ? limit 2`; a missing id throws ModelNotFoundException (the Go
// read reports errors.OrmRecordNotFound); a model argument stands for its own key.
func (m *belongsToManyRelationQuery) FindSole(id any, dest any) error {
	return m.Builder.WhereKey(id).Sole(dest)
}

// FirstWhere adds the where clause and returns the first matching row into dest — Laravel
// BelongsToMany::firstWhere (BelongsToMany.php:853-856,
// `$this->where($column, $operator, $value, $boolean)->first()`). The clause lands on the same
// pivot-aware builder the relation reads through; a miss leaves dest empty and reports no error,
// the First reading. Oracle (v12.55.1, executed): `... and "name" = ? limit 1`; a miss returns
// null.
func (m *belongsToManyRelationQuery) FirstWhere(dest any, column any, args ...any) error {
	return m.Builder.Where(column, args...).First(dest)
}

// FirstOrNew returns the first related row matching attributes, or a new instance carrying
// attributes+values (values win) — Laravel BelongsToMany::firstOrNew (vendor v12.55.1,
// BelongsToMany.php:637): the search is $this->related->where($attributes)->first(), the plain
// related-table read, never the pivot join, so no Pivot field is hydrated and no pivot row is
// written (the attach is a later, explicit write). Shell: FirstOrNewRelation's Many2Many branch
// (database/gorm/relation_writes.go).
func (m *belongsToManyRelationQuery) FirstOrNew(dest any, attributes any, values ...any) error {
	return m.Builder.FirstOrNewRelation(m.parent, m.name, attributeMap(attributes), attributeMapFrom(values...), dest)
}

// FirstOrCreate is FirstOrNew that persists a miss through createOrFirst — Laravel
// BelongsToMany::firstOrCreate (vendor v12.55.1, BelongsToMany.php:655) probes three levels: the
// pivot-joined relation read (:655/:657; an attached hit returns carrying its Pivot values), the
// plain related read (:658; a hit is attached inside a savepoint, the already-attached violation
// swallowed, :661-664), and finally createOrFirst (:659). Shell: FirstOrCreateRelation's Many2Many
// branch (database/gorm/relation_writes.go, firstOrCreateMany2Many).
func (m *belongsToManyRelationQuery) FirstOrCreate(dest any, conds ...any) error {
	return m.Builder.FirstOrCreateRelation(m.parent, m.name, attributeMapFrom(conds...), attributeMapAt(conds, 1), dest, m.pivotValues...)
}

// UpdateOrCreate is FirstOrCreate followed by a conditional fill+save — Laravel
// BelongsToMany::updateOrCreate (vendor v12.55.1, BelongsToMany.php:707): tap(firstOrCreate(...))
// and, when the instance was not recently created, fill($values) + save(['touch' => false])
// (:709-712) — so a related row that exists but is not yet attached is attached and updated, an
// attached hit is updated (its Pivot kept), and a fresh create is left as its insert wrote it.
// Shell: UpdateOrCreateRelation's Many2Many branch (database/gorm/relation_writes.go, over
// firstOrCreateMany2Many).
func (m *belongsToManyRelationQuery) UpdateOrCreate(dest any, attributes any, values any) error {
	return m.Builder.UpdateOrCreateRelation(m.parent, m.name, attributeMap(attributes), attributeMap(values), dest, m.pivotValues...)
}

// CreateOrFirst attempts the related insert immediately — Laravel BelongsToMany::createOrFirst
// (vendor v12.55.1, BelongsToMany.php:681): the insert carries the pivot attach (via
// BelongsToMany::create, :1417) inside withSavepointIfNeeded; a unique violation reads the
// related row back by attributes and attaches it (:690-691); a read-back miss rethrows into the
// second catch (:693), and both that and a pivot violation — the row already exists — re-read
// the row through the pivot-joined relation on the write PDO (:694), the read that hydrates
// the Pivot field. Shell: CreateOrFirstRelation's
// Many2Many branch (database/gorm/relation_writes.go, createOrFirstMany2Many).
func (m *belongsToManyRelationQuery) CreateOrFirst(dest any, conds ...any) error {
	return m.Builder.CreateOrFirstRelation(m.parent, m.name, attributeMapFrom(conds...), attributeMapAt(conds, 1), dest, m.pivotValues...)
}

func (m *belongsToManyRelationQuery) Save(child any) error {
	return m.Builder.SaveRelation(m.parent, m.name, child, m.pivotValues...)
}

// SaveQuietly is Save with every model event suppressed — Laravel's BelongsToMany::saveQuietly
// (vendor v12.55.1, BelongsToMany.php:1366), save inside Model::withoutEvents (Concerns/
// HasEvents.php:445). The oracle (v12.55.1, executed; /tmp/lalign_write_quiet_force.php) measured
// zero events on the quiet call while the related row and the pivot row still land and the model
// is returned; the pivot attach and the parent touch raise no model events on either path.
func (m *belongsToManyRelationQuery) SaveQuietly(child any) error {
	return m.Builder.SaveRelationQuietly(m.parent, m.name, child, m.pivotValues...)
}

func (m *belongsToManyRelationQuery) SaveMany(children any) error {
	return m.Builder.SaveManyRelation(m.parent, m.name, children, m.pivotValues...)
}

// SaveManyQuietly is the slice form of SaveQuietly — BelongsToMany::saveManyQuietly (:1402).
func (m *belongsToManyRelationQuery) SaveManyQuietly(children any) error {
	return m.Builder.SaveManyRelationQuietly(m.parent, m.name, children, m.pivotValues...)
}

func (m *belongsToManyRelationQuery) SaveWithPivot(child any, attrs map[string]any) error {
	return m.Builder.SaveRelationWithPivot(m.parent, m.name, child, attrs, m.pivotValues...)
}

func (m *belongsToManyRelationQuery) SaveManyWithPivot(children any, attrsPerChild map[any]map[string]any) error {
	return m.Builder.SaveManyRelationWithPivot(m.parent, m.name, children, attrsPerChild, m.pivotValues...)
}

func (m *belongsToManyRelationQuery) Create(dest any) error {
	return m.Builder.CreateRelation(m.parent, m.name, dest, m.pivotValues...)
}

func (m *belongsToManyRelationQuery) CreateMany(dests any) error {
	return m.Builder.CreateManyRelation(m.parent, m.name, dests, m.pivotValues...)
}

func (m *belongsToManyRelationQuery) Attach(ids []any) error {
	return m.Builder.AttachRelation(m.parent, m.name, ids, m.pivotValues...)
}

// AttachOrFail is Attach inside a transaction on the parent's connection — IWPT::attachOrFail
// (InteractsWithPivotTable.php:365; a 12.55-only addition). The oracle (v12.55.1, executed;
// /tmp/lalign_write_or_fail.php) measured begin+commit around the call, a begin+rollback pair
// when the insert fails, and the void return.
func (m *belongsToManyRelationQuery) AttachOrFail(ids []any) error {
	return m.Builder.AttachRelationOrFail(m.parent, m.name, ids, m.pivotValues...)
}

func (m *belongsToManyRelationQuery) AttachWithPivot(idsWithAttrs map[any]map[string]any) error {
	return m.Builder.AttachWithPivotRelation(m.parent, m.name, idsWithAttrs, m.pivotValues...)
}

func (m *belongsToManyRelationQuery) Detach(ids ...any) (int64, error) {
	return m.Builder.DetachRelation(m.parent, m.name, ids)
}

// DetachOrFail is Detach inside a parent-connection transaction — IWPT::detachOrFail (:562); the
// oracle measured begin+commit, the removed-row count, and detachOrFail(null) detaching every row
// of the parent.
func (m *belongsToManyRelationQuery) DetachOrFail(ids ...any) (int64, error) {
	return m.Builder.DetachRelationOrFail(m.parent, m.name, ids)
}

func (m *belongsToManyRelationQuery) Sync(ids []any) (*dbcontract.SyncResult, error) {
	return m.Builder.SyncRelation(m.parent, m.name, ids, m.pivotValues...)
}

// SyncOrFail is Sync inside a parent-connection transaction — IWPT::syncOrFail (:157); the oracle
// measured begin+commit and the same changes shape Sync reports.
func (m *belongsToManyRelationQuery) SyncOrFail(ids []any) (*dbcontract.SyncResult, error) {
	return m.Builder.SyncRelationOrFail(m.parent, m.name, ids, m.pivotValues...)
}

func (m *belongsToManyRelationQuery) SyncWithPivot(idsWithAttrs map[any]map[string]any) (*dbcontract.SyncResult, error) {
	return m.Builder.SyncRelationWithPivot(m.parent, m.name, idsWithAttrs, m.pivotValues...)
}

func (m *belongsToManyRelationQuery) SyncWithPivotValues(ids []any, pivotValues map[string]any) (*dbcontract.SyncResult, error) {
	return m.Builder.SyncRelationWithPivotValues(m.parent, m.name, ids, pivotValues, m.pivotValues...)
}

// SyncWithPivotValuesOrFail is SyncWithPivotValues inside a parent-connection transaction —
// IWPT::syncWithPivotValuesOrFail (:200).
func (m *belongsToManyRelationQuery) SyncWithPivotValuesOrFail(ids []any, pivotValues map[string]any) (*dbcontract.SyncResult, error) {
	return m.Builder.SyncRelationWithPivotValuesOrFail(m.parent, m.name, ids, pivotValues, m.pivotValues...)
}

func (m *belongsToManyRelationQuery) SyncWithoutDetaching(ids []any) (*dbcontract.SyncResult, error) {
	return m.Builder.SyncWithoutDetachingRelation(m.parent, m.name, ids, m.pivotValues...)
}

// SyncWithoutDetachingOrFail is SyncWithoutDetaching inside a parent-connection transaction —
// IWPT::syncWithoutDetachingOrFail (:170), Laravel's syncOrFail($ids, false).
func (m *belongsToManyRelationQuery) SyncWithoutDetachingOrFail(ids []any) (*dbcontract.SyncResult, error) {
	return m.Builder.SyncWithoutDetachingRelationOrFail(m.parent, m.name, ids, m.pivotValues...)
}

func (m *belongsToManyRelationQuery) SyncWithoutDetachingWithPivot(idsWithAttrs map[any]map[string]any) (*dbcontract.SyncResult, error) {
	return m.Builder.SyncWithoutDetachingRelationWithPivot(m.parent, m.name, idsWithAttrs, m.pivotValues...)
}

func (m *belongsToManyRelationQuery) Toggle(ids []any) (*dbcontract.SyncResult, error) {
	return m.Builder.ToggleRelation(m.parent, m.name, ids, m.pivotValues...)
}

// ToggleOrFail is Toggle inside a parent-connection transaction — IWPT::toggleOrFail (:75); the
// oracle measured begin+commit and the attached/detached change lists.
func (m *belongsToManyRelationQuery) ToggleOrFail(ids []any) (*dbcontract.SyncResult, error) {
	return m.Builder.ToggleRelationOrFail(m.parent, m.name, ids, m.pivotValues...)
}

func (m *belongsToManyRelationQuery) ToggleWithPivot(idsWithAttrs map[any]map[string]any) (*dbcontract.SyncResult, error) {
	return m.Builder.ToggleRelationWithPivot(m.parent, m.name, idsWithAttrs, m.pivotValues...)
}

func (m *belongsToManyRelationQuery) UpdateExistingPivot(id any, attrs map[string]any) (int64, error) {
	return m.Builder.UpdateExistingPivotRelation(m.parent, m.name, id, attrs)
}

// UpdateExistingPivotOrFail is UpdateExistingPivot inside a parent-connection transaction —
// IWPT::updateExistingPivotOrFail (:299); the oracle measured begin+commit and the updated count.
func (m *belongsToManyRelationQuery) UpdateExistingPivotOrFail(id any, attrs map[string]any) (int64, error) {
	return m.Builder.UpdateExistingPivotRelationOrFail(m.parent, m.name, id, attrs)
}

// AllRelatedIds collects the related ids into dest — a pointer to a slice — Eloquent's
// BelongsToMany::allRelatedIds. Laravel runs a fresh pivot statement there (oracle,
// illuminate/database v12.55.1, sqlite, over the corpus's morphToMany post->tags relation:
// `select "tag_id" from "taggables" where <replayed pivot wheres, raw> and
// "taggables"."taggable_id" = ? and "taggable_type" = ?`). The object layer cannot construct a
// fresh statement — the Query contract exposes no newQuery/from entry point, and the pivot write
// shells resolve their own statements per call — so the Go form plucks the related pivot key
// through the pivot-aware relation read: the statement Laravel's `$relation->pluck($relatedPivotKey)`
// compiles (oracle: `select "tag_id" from "tags" inner join "taggables" on "tags"."id" =
// "taggables"."tag_id" where "taggables"."taggable_id" = ? and "taggables"."taggable_type" = ?`,
// modulo the join rendering shared by every relation read in the port), carrying every call-time
// wherePivot clause already recorded on this object. Documented divergence: the join drops pivot
// rows whose related row is missing and applies the related model's global scopes, where Laravel's
// pivot statement has neither.
func (m *belongsToManyRelationQuery) AllRelatedIds(dest any) error {
	return m.Builder.Pluck(m.info.PivotRelatedRef.ForeignColumn, dest)
}

// --- take / limit ---------------------------------------------------------------------------
//
// BelongsToMany::take (vendor v12.55.1, BelongsToMany.php:1492) and BelongsToMany::limit (:1503):
//
//	public function limit($value)
//	{
//	    if ($this->parent->exists) {
//	        $this->query->limit($value);
//	    } else {
//	        $column = $this->getExistenceCompareKey();
//
//	        $grammar = $this->query->getQuery()->getGrammar();
//
//	        if ($grammar instanceof MySqlGrammar && $grammar->useLegacyGroupLimit($this->query->getQuery())) {
//	            $column = 'pivot_'.last(explode('.', $column));
//	        }
//
//	        $this->query->groupLimit($value, $column);
//	    }
//
//	    return $this;
//	}
//
// An existing parent keeps the plain query limit — the relation read's own `... where
// "pivot"."parent_fk" = ? limit N`. A parent that does not exist — the prototype an eager-load
// constraint runs the relation on (Relation::noConstraints(fn () => $this->getModel()
// ->newInstance()->$name())) — caps the rows per parent instead, through the query builder's group
// limit: the row_number() over (partition by <qualified foreign pivot key>) derived table, so
// with(['roles' => fn ($q) => $q->limit(2)]) reads two roles for every parent. The partition column
// is the qualified foreign pivot key (BelongsToMany::getExistenceCompareKey, BelongsToMany.php:1527,
// `return $this->getQualifiedForeignPivotKeyName();`); on the legacy MySQL rewrite it becomes the
// aliased pivot column ('pivot_'.last(explode('.', $column)), :1512). Oracle (illuminate/database
// v12.55.1, executed against sqlite from /tmp/eager-m2m-limit/probe.php): B1 eager BtM limit(2) ->
// `select * from (select "m2m_roles".*, "m2m_role_user"."user_id" as "pivot_user_id",
// "m2m_role_user"."role_id" as "pivot_role_id", "m2m_role_user"."note" as "pivot_note",
// row_number() over (partition by "m2m_role_user"."user_id") as "laravel_row" from "m2m_roles"
// inner join "m2m_role_user" on "m2m_roles"."id" = "m2m_role_user"."role_id" where
// "m2m_role_user"."user_id" in (1, 2)) as "laravel_table" where "laravel_row" <= 2 order by
// "laravel_row"`, counts [2, 2]; B2 take(2) compiles the same rewrite; B3 a non-existing parent's
// toBase() reports limit NULL and groupLimit ['value' => 2, 'column' => 'm2m_role_user.user_id'];
// B4 an existing parent's relation read carries the plain `... where "m2m_role_user"."user_id" = ?
// limit 2` with groupLimit NULL.

// Take is the alias of Limit — BelongsToMany::take (BelongsToMany.php:1492).
func (m *belongsToManyRelationQuery) Take(value int) contractsorm.Query {
	return m.Limit(value)
}

// Limit sets the relation's limit — BelongsToMany::limit (BelongsToMany.php:1503): the plain query
// limit for an existing parent, the per-parent group limit on getExistenceCompareKey() otherwise
// (the aliased pivot column on the legacy MySQL rewrite, pinned on the real MySQL grammar given a
// 5.7.44 server version: tests/database/gorm/dialect_shapes_test.go, oracle /tmp/r15bd/legacy.php
// E1). The relation object is returned, as Laravel's `return $this`.
func (m *belongsToManyRelationQuery) Limit(value int) contractsorm.Query {
	if m.parentExists() {
		m.rebuild(m.Builder.Limit(value))
	} else {
		column := m.GetExistenceCompareKey()
		if m.Builder.UseLegacyGroupLimit() {
			column = "pivot_" + lastColumnSegment(column)
		}

		m.rebuild(m.Builder.GroupLimit(value, column))
	}

	return m
}

// GetExistenceCompareKey gets the key the existence query compares the parent key against —
// BelongsToMany::getExistenceCompareKey (vendor v12.55.1, BelongsToMany.php:1527,
// `return $this->getQualifiedForeignPivotKeyName();`): the pivot's foreign key qualified with the
// pivot table (oracle B3: 'm2m_role_user.user_id').
func (m *belongsToManyRelationQuery) GetExistenceCompareKey() string {
	return m.pivotQualified(m.info.PivotParentRef.ForeignColumn)
}

// --- the pivot-column query family ------------------------------------------
//
// The family mirrors Eloquent's BelongsToMany wherePivot surface: each method appends one clause
// on the pivot table to the relation's read query. Laravel's wherePivot lands as a WHERE on the
// qualified pivot column in the relation read (and an ORDER BY for orderByPivot; oracle,
// illuminate/database v12.55.1, sqlite, over a morphToMany post->tags relation — NOT in the
// join's ON clause, which was verified shape by shape):
//
//	wherePivot('type','t1')          and "taggables"."type" = ?
//	orWherePivot                     or "taggables"."type" = ?
//	wherePivotIn('type',[t1,t2])     and "taggables"."type" in (?, ?)
//	wherePivotNotIn                  and "taggables"."type" not in (?)
//	orWherePivotIn / NotIn           or "taggables"."type" in (?) / not in (?)
//	wherePivotNull                   and "taggables"."type" is null
//	wherePivotNotNull                and "taggables"."type" is not null
//	orWherePivotNull                 or "taggables"."type" is null
//	orWherePivotNotNull              or "taggables"."type" is not null
//	wherePivotBetween                and "taggables"."type" between ? and ?
//	wherePivotNotBetween             and "taggables"."type" not between ? and ?
//	orWherePivotBetween/NotBetween   or "taggables"."type" between ? and ? / not between ? and ?
//	withPivotValue('type','t1')      and "taggables"."type" = ?
//	orderByPivot('type')             order by "taggables"."type" asc
//
// A column argument is `any` the way Laravel's qualifyPivotColumn takes one: a database/query
// Expression passes through untouched — the grammar splices it verbatim, unqualified — while a
// plain string is qualified with the pivot table unless it already carries a qualifier. The clause
// kinds whose Query entry point takes a plain string column (WhereIn / WhereNull / WhereBetween /
// OrderBy) splice an Expression through the Raw counterparts instead, so it rides the statement
// exactly as Laravel's call renders it (oracle: wherePivot($expr,'=','v') -> `and
// CONCAT(foo, '_', bar) = ?`; wherePivotBetween -> `... between ? and ?`; wherePivotIn ->
// `... in (?)`; wherePivotNull -> `... is null`; orderByPivot -> `order by CONCAT(foo, '_', bar)
// asc`; an empty value list renders Laravel's constants — `and 0 = 1` for in, `and 1 = 1` for
// not in).
//
// Documented divergences (vs the same oracle): a plain-string orWherePivotNotNull rides a
// one-clause nested group — `or ("taggables"."type" is not null)` — because the Query contract
// declares no OrWhereNotNull, where Laravel renders the paren-less form (the Expression path is
// byte-equal). Laravel's pivotWheres/pivotWhereIns/pivotWhereNulls replay (newPivotQuery) also
// reaches the pivot statements of detach / updateExistingPivot / sync's currently-attached read;
// those write shells resolve their own pivot statements per call and cannot see these call-time
// clauses — only the relation read and AllRelatedIds, which ride this builder, observe them.

// pivotColumn resolves a pivot-family column argument the way Laravel's qualifyPivotColumn does: a
// database/query Expression passes through untouched, while a plain string is qualified with the
// pivot table (an already-qualified string passes through).
func (m *belongsToManyRelationQuery) pivotColumn(column any) any {
	if querybuilder.IsExpression(column) {
		return column
	}

	return m.pivotQualified(column)
}

// pivotPlaceholders renders one "?" per value, comma-separated — the right-hand side Laravel's
// whereIn parameterizes an Expression column into.
func pivotPlaceholders(count int) string {
	if count == 0 {
		return ""
	}

	return strings.Repeat("?, ", count-1) + "?"
}

// pivotInClause renders the raw "(not) in" clause an Expression column produces: the expression's
// SQL verbatim with one placeholder per value — the statement Laravel's whereIn renders — or the
// empty-list constant Laravel compiles (0 = 1 matches nothing, 1 = 1 matches everything).
func pivotInClause(column string, values []any, not bool) (string, []any) {
	if len(values) == 0 {
		if not {
			return "1 = 1", nil
		}

		return "0 = 1", nil
	}

	keyword := "in"
	if not {
		keyword = "not in"
	}

	return column + " " + keyword + " (" + pivotPlaceholders(len(values)) + ")", values
}

func (m *belongsToManyRelationQuery) WherePivot(column any, args ...any) contractsorm.BelongsToManyRelation {
	m.rebuild(m.Builder.Where(m.pivotColumn(column), args...))

	return m
}

func (m *belongsToManyRelationQuery) OrWherePivot(column any, args ...any) contractsorm.BelongsToManyRelation {
	m.rebuild(m.Builder.OrWhere(m.pivotColumn(column), args...))

	return m
}

func (m *belongsToManyRelationQuery) WherePivotIn(column any, values []any) contractsorm.BelongsToManyRelation {
	if querybuilder.IsExpression(column) {
		clause, args := pivotInClause(querybuilder.ExpressionValue(column), values, false)
		m.rebuild(m.Builder.WhereRaw(clause, args...))

		return m
	}

	m.rebuild(m.Builder.WhereIn(m.pivotQualified(column), values))

	return m
}

func (m *belongsToManyRelationQuery) WherePivotNotIn(column any, values []any) contractsorm.BelongsToManyRelation {
	if querybuilder.IsExpression(column) {
		clause, args := pivotInClause(querybuilder.ExpressionValue(column), values, true)
		m.rebuild(m.Builder.WhereRaw(clause, args...))

		return m
	}

	m.rebuild(m.Builder.WhereNotIn(m.pivotQualified(column), values))

	return m
}

func (m *belongsToManyRelationQuery) OrWherePivotIn(column any, values []any) contractsorm.BelongsToManyRelation {
	if querybuilder.IsExpression(column) {
		clause, args := pivotInClause(querybuilder.ExpressionValue(column), values, false)
		m.rebuild(m.Builder.OrWhereRaw(clause, args...))

		return m
	}

	m.rebuild(m.Builder.OrWhereIn(m.pivotQualified(column), values))

	return m
}

func (m *belongsToManyRelationQuery) OrWherePivotNotIn(column any, values []any) contractsorm.BelongsToManyRelation {
	if querybuilder.IsExpression(column) {
		clause, args := pivotInClause(querybuilder.ExpressionValue(column), values, true)
		m.rebuild(m.Builder.OrWhereRaw(clause, args...))

		return m
	}

	m.rebuild(m.Builder.OrWhereNotIn(m.pivotQualified(column), values))

	return m
}

func (m *belongsToManyRelationQuery) WherePivotNull(column any) contractsorm.BelongsToManyRelation {
	if querybuilder.IsExpression(column) {
		m.rebuild(m.Builder.WhereRaw(querybuilder.ExpressionValue(column) + " is null"))

		return m
	}

	m.rebuild(m.Builder.WhereNull(m.pivotQualified(column)))

	return m
}

func (m *belongsToManyRelationQuery) WherePivotNotNull(column any) contractsorm.BelongsToManyRelation {
	if querybuilder.IsExpression(column) {
		m.rebuild(m.Builder.WhereRaw(querybuilder.ExpressionValue(column) + " is not null"))

		return m
	}

	m.rebuild(m.Builder.WhereNotNull(m.pivotQualified(column)))

	return m
}

func (m *belongsToManyRelationQuery) OrWherePivotNull(column any) contractsorm.BelongsToManyRelation {
	if querybuilder.IsExpression(column) {
		m.rebuild(m.Builder.OrWhereRaw(querybuilder.ExpressionValue(column) + " is null"))

		return m
	}

	m.rebuild(m.Builder.OrWhereNull(m.pivotQualified(column)))

	return m
}

// OrWherePivotNotNull rides a single-clause nested group ("or (col is not null)") on the plain
// path: the query contract declares OrWhereNull but no or-not-null clause, and the one-clause
// group is semantically equivalent. Laravel renders the paren-less `or "pivot"."col" is not null`
// (oracle-pinned) — the documented divergence above; an Expression column is rendered byte-equal
// through OrWhereRaw.
func (m *belongsToManyRelationQuery) OrWherePivotNotNull(column any) contractsorm.BelongsToManyRelation {
	if querybuilder.IsExpression(column) {
		m.rebuild(m.Builder.OrWhereRaw(querybuilder.ExpressionValue(column) + " is not null"))

		return m
	}

	qualified := m.pivotQualified(column)
	m.rebuild(m.Builder.OrWhere(func(query contractsorm.Query) contractsorm.Query {
		return query.WhereNotNull(qualified)
	}))

	return m
}

func (m *belongsToManyRelationQuery) WherePivotBetween(column any, x, y any) contractsorm.BelongsToManyRelation {
	if querybuilder.IsExpression(column) {
		m.rebuild(m.Builder.WhereRaw(querybuilder.ExpressionValue(column)+" between ? and ?", x, y))

		return m
	}

	m.rebuild(m.Builder.WhereBetween(m.pivotQualified(column), x, y))

	return m
}

func (m *belongsToManyRelationQuery) WherePivotNotBetween(column any, x, y any) contractsorm.BelongsToManyRelation {
	if querybuilder.IsExpression(column) {
		m.rebuild(m.Builder.WhereRaw(querybuilder.ExpressionValue(column)+" not between ? and ?", x, y))

		return m
	}

	m.rebuild(m.Builder.WhereNotBetween(m.pivotQualified(column), x, y))

	return m
}

func (m *belongsToManyRelationQuery) OrWherePivotBetween(column any, x, y any) contractsorm.BelongsToManyRelation {
	if querybuilder.IsExpression(column) {
		m.rebuild(m.Builder.OrWhereRaw(querybuilder.ExpressionValue(column)+" between ? and ?", x, y))

		return m
	}

	m.rebuild(m.Builder.OrWhereBetween(m.pivotQualified(column), x, y))

	return m
}

func (m *belongsToManyRelationQuery) OrWherePivotNotBetween(column any, x, y any) contractsorm.BelongsToManyRelation {
	if querybuilder.IsExpression(column) {
		m.rebuild(m.Builder.OrWhereRaw(querybuilder.ExpressionValue(column)+" not between ? and ?", x, y))

		return m
	}

	m.rebuild(m.Builder.OrWhereNotBetween(m.pivotQualified(column), x, y))

	return m
}

// WithPivotValue adds a "pivot column = value" clause and records the pair for the pivot
// construction — the Go form of BelongsToMany::withPivotValue (vendor v12.55.1,
// BelongsToMany.php:481-498, `$this->pivotValues[] = compact('column', 'value'); return
// $this->wherePivot($column, '=', $value);`): the pair lives per relation instance as PHP keeps
// $pivotValues, and base NewPivot merges the recorded values into its attribute bag
// (IWPT::newPivot, InteractsWithPivotTable.php:629 — see NewPivot below). The pair recorded is
// the one this call applied: Laravel 12 throws InvalidArgumentException for a null value
// (oracle-probed: "The provided value may not be null."), while the frozen Go contract keeps the
// pre-12 argument swap for that shape — a nil value pairs the argument column with the pivot's
// parent-side foreign key column — so `WithPivotValue(7, nil)` clauses the parent pivot key
// against 7 and records that pair. The morph leaf's promoted call records here too; its NewPivot
// override never merges the list (MorphToMany.php:156-172), but every attach path does — the
// recorded pairs ride the pivot INSERT through baseAttachRecord's values loop (:467), which the
// attach / attachOrFail / sync / toggle shells now consume: the pairs stamp the row between the
// timestamps and the caller's attributes (recorded defaults, the caller wins, the last recorded
// pair for a column winning). Oracle v12.55.1, executed, /tmp/r7pv/attach_pv_probe.php A1-A7:
// base attach after withPivotValue -> `insert into "club_user" ("club_id", "is_admin", "user_id")
// values (?, ?, ?)` binding 'gold'; morph attach -> `insert into "taggables" ("tag_id",
// "taggable_id", "taggable_type", "tier") values (?, ?, ?, ?)` binding 'gold'.
func (m *belongsToManyRelationQuery) WithPivotValue(column any, value any) contractsorm.BelongsToManyRelation {
	if value == nil {
		column, value = m.info.PivotParentRef.ForeignColumn, column
	}

	m.pivotValues = append(m.pivotValues, PivotValue{Column: column, Value: value})

	return m.WherePivot(column, "=", value)
}

// OrderByPivot adds an "order by pivot column" clause. Mirrors BelongsToMany::orderByPivot; the
// Go object keeps the relation (PHP returns the builder), so further wherePivot calls stay on the
// object. An Expression column rides OrderByRaw — the OrderBy entry point wraps its column as an
// identifier — and an invalid direction records the same error the plain path's OrderBy does.
func (m *belongsToManyRelationQuery) OrderByPivot(column any, direction ...string) contractsorm.BelongsToManyRelation {
	if querybuilder.IsExpression(column) {
		order := "asc"
		if len(direction) > 0 {
			order = strings.ToLower(direction[0])
		}
		if order != "asc" && order != "desc" {
			// Delegate the validation (and the recorded error) to OrderBy; its column never
			// renders on the error path.
			m.rebuild(m.Builder.OrderBy("", direction...))

			return m
		}

		m.rebuild(m.Builder.OrderByRaw(querybuilder.ExpressionValue(column) + " " + order))

		return m
	}

	m.rebuild(m.Builder.OrderBy(m.pivotQualified(column), direction...))

	return m
}

// --- the pivot accessor surface ----------------------------------------------
//
// WithPivot / HasPivotColumn / GetPivotColumns / GetPivotAccessor / As carry Eloquent's call-time
// pivot-accessor state on the object (IWPT::$pivotColumns + BelongsToMany::$accessor) and push it
// through the optional pivotReadConfigurer capability so the relation read's select list and
// hydration plan follow it — the state aliasedPivotColumns() and hydratePivotRelation() read at
// get() time in Laravel. Every behavior below was pinned by the oracle (illuminate/database
// v12.55.1, sqlite, /tmp/lalign-probes/pivot_surface.out; the .php sibling keeps the probe).

// pivotReadConfigurer is the optional builder capability the leaf uses to apply its pivot-read
// state to the wrapped builder. database/gorm's OrmBuilder implements it (ConfigurePivotRead); a
// builder without the capability (nil, a unit-test stub, a foreign driver) keeps the state on the
// object alone, so its reads keep the declared shape.
type pivotReadConfigurer interface {
	ConfigurePivotRead(accessor string, columns []string) contractsorm.Query
}

// applyPivotRead pushes the leaf's accessor + column list to the wrapped builder and keeps the
// object's handle in step with the returned one (the guarded handle an unresolvable accessor
// yields).
func (m *belongsToManyRelationQuery) applyPivotRead() {
	configurer, ok := m.Builder.(pivotReadConfigurer)
	if !ok {
		return
	}

	m.rebuild(configurer.ConfigurePivotRead(m.pivotAccessor, m.pivotColumns))
}

// OrderByPivotDesc adds a descending "order by pivot column" clause — BelongsToMany
// ::orderByPivotDesc (vendor v12.55.1, BelongsToMany.php:605-608; 12.55-only — absent from the
// v12.43.1 checkout). The one-liner over OrderByPivot is exactly Laravel's body,
// `$this->orderBy($this->qualifyPivotColumn($column), 'desc')` with the direction parameter
// already bound (oracle P8: `... order by "lax_role_user"."level" desc`).
func (m *belongsToManyRelationQuery) OrderByPivotDesc(column any) contractsorm.BelongsToManyRelation {
	return m.OrderByPivot(column, "desc")
}

// WithPivot appends columns to the pivot-column list the relation read retrieves — see the
// contract for the full Laravel mapping (IWPT::withPivot, vendor v12.55.1 :702-707; oracle
// /tmp/lalign-probes/pivot_surface.out P2/P3/P4: the read statement gains
// `"lax_role_user"."level" as "pivot_level"`, the raw list keeps duplicates while the SELECT
// deduplicates, chained calls append in call order; P7: writes never replay the list).
func (m *belongsToManyRelationQuery) WithPivot(columns ...string) contractsorm.BelongsToManyRelation {
	if len(columns) > 0 {
		m.pivotColumns = append(m.pivotColumns, columns...)
		m.applyPivotRead()
	}

	return m
}

// HasPivotColumn reports whether column was explicitly registered through WithPivot — IWPT
// ::hasPivotColumn (:508-510, `in_array($column, $this->pivotColumns)`; oracle P1a: the default
// pivot keys are not members; P9: a key passed to WithPivot explicitly is).
func (m *belongsToManyRelationQuery) HasPivotColumn(column string) bool {
	return slices.Contains(m.pivotColumns, column)
}

// GetPivotColumns gets the raw explicit pivot-column list — BelongsToMany::getPivotColumns
// (:1689-1691, `return $this->pivotColumns;`; oracle P3: duplicates kept; P4: merged in call
// order; P1a: empty by default).
func (m *belongsToManyRelationQuery) GetPivotColumns() []string {
	if m.pivotColumns == nil {
		return nil
	}

	// A fresh copy — PHP returns arrays by value, so a caller mutating the result cannot reach
	// the object's own list.
	return append([]string(nil), m.pivotColumns...)
}

// GetPivotAccessor gets the name the pivot hydration lands under — BelongsToMany::getPivotAccessor
// (:1679-1681; oracle P1a: the class default 'pivot'). The Go form reports the hydration field
// name: the As override, else the declared PivotField (default "Pivot", the Go stand-in for
// 'pivot'); "" when the projection names no field.
func (m *belongsToManyRelationQuery) GetPivotAccessor() string {
	if m.pivotAccessor != "" {
		return m.pivotAccessor
	}

	return m.info.PivotField
}

// As declares the name the pivot hydration lands under — BelongsToMany::as (:367-372; oracle P5:
// the accessor renames where the pivot lands — $role->subscription, $role->pivot null — and never
// the SQL aliases, which stay pivot_-prefixed). In Go the accessor names the struct field the
// read hydrates: As re-resolves the read's pivot plan against that field, exactly like the
// declaration-time PivotField resolution — no field by the name hydrates nothing (the read still
// runs), a field that is not a struct surfaces errors.OrmRelationPivotFieldNotStruct on the read,
// and an empty accessor restores the declared field.
func (m *belongsToManyRelationQuery) As(accessor string) contractsorm.BelongsToManyRelation {
	m.pivotAccessor = accessor
	m.applyPivotRead()

	return m
}

// --- the pivot construction surface ------------------------------------------
//
// Using / NewPivot / NewExistingPivot are the relation's pivot-constructor family — Eloquent's
// BelongsToMany::using plus InteractsWithPivotTable::newPivot / newExistingPivot, the methods the
// relation's reads and writes construct pivots with (Laravel's hydratePivotRelation feeds
// newExistingPivot; attach's custom-class path feeds newPivot). In Go the constructor family is
// what the Using factory observably changes: the read path's field hydration stays the declared
// pivot struct's, and the write shells keep their per-call statements — Laravel's custom-class
// write dispatch (attachUsingCustomClass :377 / updateExistingPivotUsingCustomClass :312 /
// detachUsingCustomClass :573) is deferred whole, with the probe's statement shapes recorded in
// the fix report. Every behavior below was pinned by the oracle (illuminate/database v12.55.1,
// sqlite, /tmp/decide-light-using/using_probe.php).

// Using records the pivot factory the relation's pivot-construction methods dispatch through —
// see the contract for the full Laravel mapping (BelongsToMany::using, vendor v12.55.1,
// BelongsToMany.php:350-355). The state lives on this relation object alone, exactly as Laravel
// keeps $using per relation instance; nil clears it back to the default constructors.
func (m *belongsToManyRelationQuery) Using(factory contractsorm.PivotFactory) contractsorm.BelongsToManyRelation {
	m.pivotFactory = factory

	return m
}

// NewPivot builds a pivot instance for the relation — InteractsWithPivotTable::newPivot (vendor
// v12.55.1, InteractsWithPivotTable.php:627-642). The WithPivotValue-recorded values merge into
// the attributes FIRST — `array_merge(array_column($this->pivotValues, 'value', 'column'),
// $attributes)` (:629): the recorded values are defaults, the caller's attributes win, and for a
// duplicated column the last recorded pair wins. A recorded Using factory then receives
// (parent, attributes, table, exists) and its result is returned — the Go reading of `$using
// ::fromRawAttributes(...)` through Model::newPivot (Model.php:1726); the class default is
// relations.Pivot::FromAttributes, then the relation stamps its pivot keys and related model
// (`setPivotKeys($this->foreignPivotKey, $this->relatedPivotKey)->setRelatedModel($this->related)`,
// :640-642). Oracle (v12.55.1, executed): `U2a default newPivot class/exists => [Pivot, false]`,
// `U2a default newPivot dirty => []`, `U2a default newPivot table/keys => ['lu_role_user',
// 'lu_user_id', 'role_id']`, `U2c custom newPivot class/exists => ['LuRoleUser', false]`,
// `U2c custom newPivot dirty => ['level' => 'gold']`; the withPivotValue extension
// (/tmp/reverify429/pv_probe.php): `C21 base attrs => ['level' => 'pv']`, `C21 base dirty => []`,
// `C21 caller wins => ['level' => 'caller']`, `C21 caller extra => ['level' => 'pv', 'other' =>
// 'x']`, `C21 duplicate last wins => ['level' => 'b']`, `C21 base existing attrs => ['level' =>
// 'pv']`, `C21 using-class attrs => ['level' => 'pv']`, `C21 using-class dirty => ['level' =>
// 'pv']`, `C21 morph attrs => ['taggable_type' => 'PvPost']`.
func (m *belongsToManyRelationQuery) NewPivot(attributes map[string]any, exists bool) (contractsorm.PivotModel, error) {
	attributes = m.mergePivotValues(attributes)

	if m.pivotFactory != nil {
		pivot, err := m.pivotFactory(m.parent, attributes, m.info.PivotTable, exists)
		if err != nil {
			return nil, err
		}

		return finishPivot(pivot, m.info.PivotParentRef.ForeignColumn, m.info.PivotRelatedRef.ForeignColumn, m.info.RelatedModel), nil
	}

	pivot, err := FromAttributes(m.Builder, m.parent, attributes, m.info.PivotTable, exists)
	if err != nil {
		return nil, err
	}

	return pivot.SetPivotKeys(m.info.PivotParentRef.ForeignColumn, m.info.PivotRelatedRef.ForeignColumn).
		SetRelatedModel(m.info.RelatedModel), nil
}

// NewExistingPivot is NewPivot with exists true — InteractsWithPivotTable::newExistingPivot
// (vendor v12.55.1, :646, `return $this->newPivot($attributes, true);`), the constructor
// BelongsToMany::hydratePivotRelation (BelongsToMany.php:1234) feeds migrated pivot attributes
// into (oracle: `U2b default newExistingPivot class/exists => [Pivot, true]`, `U2b default
// newExistingPivot dirty => []`, `U2d custom newExistingPivot class/exists/dirty => [LuRoleUser,
// true, []]`; the withPivotValue extension: `C21 base existing attrs => ['level' => 'pv']`).
func (m *belongsToManyRelationQuery) NewExistingPivot(attributes map[string]any) (contractsorm.PivotModel, error) {
	return m.NewPivot(attributes, true)
}

// mergePivotValues folds the WithPivotValue-recorded pairs into an attributes bag the way
// IWPT::newPivot does — `array_merge(array_column($this->pivotValues, 'value', 'column'),
// $attributes)` (vendor v12.55.1, InteractsWithPivotTable.php:629): the recorded values are
// defaults, the caller's attributes win, and for a duplicated column the last recorded pair wins
// (array_column keeps the last entry). With nothing recorded the caller's bag passes through
// untouched, so a caller's map is never mutated. A non-string recorded column cannot land on a
// Go attribute key and is skipped: PHP's array_column throws for an object column (oracle
// v12.55.1, executed: `withPivotValue(new Expression(...), 'pv')` then `newPivot` throws
// "Cannot access offset of type Illuminate\Database\Query\Expression on array"), while a
// scalar non-string is renumbered by array_merge into a nameless numeric slot
// (`withPivotValue(7, 'sev')` -> `[0 => 'sev']`; a null column dies earlier, in setAttribute) —
// either way the merge is where it cannot land. One residual edge: a numeric-string column stays
// a Go string and merges under its literal key where PHP renumbers it away (`withPivotValue('7',
// 'sev2')` -> `[0 => 'sev2']`); measured with /tmp/r6verify-c21/my_pv_probe.php.
func (m *belongsToManyRelationQuery) mergePivotValues(attributes map[string]any) map[string]any {
	if len(m.pivotValues) == 0 {
		return attributes
	}

	merged := make(map[string]any, len(m.pivotValues)+len(attributes))
	for _, entry := range m.pivotValues {
		if column, ok := entry.Column.(string); ok {
			merged[column] = entry.Value
		}
	}

	for column, value := range attributes {
		merged[column] = value
	}

	return merged
}

// finishPivot stamps a factory-built pivot whose concrete type this package knows with the
// relation's keys and related model — the post-construction stamping every pivot receives
// (IWPT:640-642). A factory result of any other type is returned untouched: a Go interface cannot
// re-dispatch the fluent setters, so a foreign custom object must return fully stamped (the
// documented Using divergence — see the contract's PivotFactory).
func finishPivot(pivot contractsorm.PivotModel, foreignKey, relatedKey string, related any) contractsorm.PivotModel {
	switch typed := pivot.(type) {
	case *Pivot:
		return typed.SetPivotKeys(foreignKey, relatedKey).SetRelatedModel(related)
	case *MorphPivot:
		// The promoted SetPivotKeys hands back the embedded *Pivot; stamp for the side effect and
		// return the concrete *MorphPivot so the morph identity survives the call.
		typed.SetPivotKeys(foreignKey, relatedKey).SetRelatedModel(related)

		return typed
	}

	return pivot
}

// CreatedAt gets the name of the "created at" column — Laravel's BelongsToMany::createdAt (vendor,
// illuminate/database v12.55.1, BelongsToMany.php:1559,
// `return $this->pivotCreatedAt ?? $this->parent->getCreatedAtColumn() ?? Model::CREATED_AT;`):
// the pivot column withTimestamps($createdAt, $updatedAt) named wins over the parent's column, and
// each of the two columns resolves on its own. The Go pivot timestamp declaration — the Pivot
// struct's autoCreateTime / autoUpdateTime tagged fields, its CreatedAt / UpdatedAt convention or
// the relation-level PivotTimestamps fallback, resolved into RelationInfo.PivotCreatedAtColumn by
// ResolvePivotTimestamps — is the object-path form of that call; an undeclared column ("") falls
// through to the base Relation::createdAt parent resolution. The PivotTimestamps: true fallback
// itself resolves the parent model's columns (Laravel's bare withTimestamps() leaves
// $pivotCreatedAt null, so createdAt() reports `$this->parent->getCreatedAtColumn() ??
// Model::CREATED_AT` and the pivot write stamps that same column), so this accessor and the write
// agree without the fallthrough. Oracle v12.55.1, executed, /tmp/btm-pivot-ts-probe.php (reads)
// and /tmp/btm-pivot-ts-write-probe.php (writes): withTimestamps('pivot_created_at',
// 'pivot_updated_at') reports and stamps 'pivot_created_at' / 'pivot_updated_at'; withTimestamps()
// and the mixed withTimestamps('pivot_created_at') report/stamp the parent class's
// 'user_created_at' / 'user_updated_at' for the sides they do not name; no withTimestamps and
// withTimestamps(false, false) stamp nothing.
func (m *belongsToManyRelationQuery) CreatedAt() string {
	if m.info.PivotCreatedAtColumn != "" {
		return m.info.PivotCreatedAtColumn
	}

	return m.relationQuery.CreatedAt()
}

// UpdatedAt is CreatedAt's updated-at sibling — BelongsToMany::updatedAt (BelongsToMany.php:1569,
// `return $this->pivotUpdatedAt ?? $this->parent->getUpdatedAtColumn() ?? Model::UPDATED_AT;`),
// over RelationInfo.PivotUpdatedAtColumn.
func (m *belongsToManyRelationQuery) UpdatedAt() string {
	if m.info.PivotUpdatedAtColumn != "" {
		return m.info.PivotUpdatedAtColumn
	}

	return m.relationQuery.UpdatedAt()
}
