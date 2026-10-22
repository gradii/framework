package query

import (
	"reflect"
	"strings"

	"github.com/goravel/framework/errors"
)

// BindingTypes is Laravel's $bindings key order. GetBindings flattens in exactly this order, which
// is what keeps positional placeholders aligned.
var BindingTypes = []string{
	"select", "from", "join", "where", "groupBy", "having", "order", "union", "unionOrder",
}

// Builder is the dialect-neutral query builder. Its state mirrors
// Illuminate\Database\Query\Builder: flat clause descriptors plus positional bindings.
//
// Presence follows Laravel's isset($query->$component): a nil slice or nil pointer means the
// component is unset, so the grammar skips it.
type Builder struct {
	grammar Grammar
	driver  string

	// useUpsertAlias mirrors the connection's "use_upsert_alias" config, which Laravel's
	// MySqlGrammar reads through $query->connection->getConfig(...) in compileUpsert. It is off by
	// default; SetUseUpsertAlias turns it on for a builder whose connection enables it.
	useUpsertAlias bool

	// err carries the first failure. Fluent methods return the builder, so errors cannot be
	// returned directly; this mirrors the existing database/db.Query builder.
	err error

	bindings map[string][]any

	aggregate        *Aggregate
	columns          []any
	distinct         bool
	distinctColumns  []any
	from             any
	joins            []*JoinClause
	wheres           []Where
	groups           []any
	havings          []Having
	orders           []Order
	limit            *int
	offset           *int
	indexHint        *IndexHint
	groupLimit       int
	groupLimitSet    bool
	groupLimitColumn string
	unions           []Union
	unionLimit       *int
	unionOffset      *int
	unionOrders      []Order
	lock             any

	// beforeQueryCallbacks queues the closures registered with BeforeQuery. They run once, at
	// the head of the next compile entry point, and the queue is then emptied. Mirrors
	// Builder::$beforeQueryCallbacks.
	beforeQueryCallbacks []func(*Builder) *Builder

	// insertUsingSet records that InsertOrIgnoreUsing stored a descriptor: the target columns, the
	// resolved subquery and its cleaned bindings that ToInsertOrIgnoreUsingSQL compiles.
	insertUsingSet      bool
	insertUsingColumns  []any
	insertUsingSQL      string
	insertUsingBindings []any

	// updateValues records the ordered assignments IncrementEach built: each column rewritten to a raw
	// `<wrapped column> + <amount>` expression, followed by the extra values. Laravel's incrementEach
	// hands the merged array to update(), which executes against its connection; the builder here holds
	// none, so the descriptor is left for the caller to compile with ToUpdateSQL.
	updateValues UpdateValues

	// insertColumns, when set, is the ordered column list an insertGetId record carries; base
	// CompileInsert uses it instead of sorting the record's keys, and insertBindings reads the values
	// in the same order. It is set only for the duration of an ordered insertGetId compile, so a
	// map-shaped insert keeps the historic sorted order.
	insertColumns []string

	// isJoinClause mirrors `$query instanceof JoinClause`. It changes the conjunction compileWheres
	// emits ("on" instead of "where") and the offset whereNested strips.
	isJoinClause bool
}

// NewBuilder creates a builder bound to a grammar.
func NewBuilder(grammar Grammar) *Builder {
	b := &Builder{grammar: grammar, bindings: make(map[string][]any, len(BindingTypes))}
	for _, t := range BindingTypes {
		b.bindings[t] = []any{}
	}
	return b
}

// NewQuery mirrors Builder::newQuery(): a fresh builder sharing the grammar and dialect.
func (b *Builder) NewQuery() *Builder {
	n := NewBuilder(b.grammar)
	n.driver = b.driver
	n.useUpsertAlias = b.useUpsertAlias
	return n
}

// GroupLimit adds a "group limit" clause to the query, used to cap the rows per group. Mirrors
// Builder::groupLimit(): a negative value is ignored and leaves any previously set group limit
// untouched, and the presence flag is what compileSelect tests (Laravel's isset($query->groupLimit)).
func (b *Builder) GroupLimit(value int, column string) *Builder {
	if value >= 0 {
		b.groupLimit = value
		b.groupLimitColumn = column
		b.groupLimitSet = true
	}

	return b
}

// SetDriver records the dialect name, used to convert placeholders at execution time.
func (b *Builder) SetDriver(driver string) { b.driver = driver }

// SetUseUpsertAlias records the connection's use_upsert_alias setting on the builder. Laravel's
// MySQL grammar reads it from the connection in compileUpsert; Go carries it on the builder, which
// represents $query, and the MySQL grammar reads it there.
func (b *Builder) SetUseUpsertAlias(alias bool) { b.useUpsertAlias = alias }

// ForSubQuery mirrors Builder::forSubQuery().
func (b *Builder) ForSubQuery() *Builder { return b.NewQuery() }

// Grammar returns the builder's grammar.
func (b *Builder) Grammar() Grammar { return b.grammar }

// Error returns the first error recorded while building, if any.
func (b *Builder) Error() error { return b.err }

// setError records the first error.
func (b *Builder) setError(err error) {
	if err != nil && b.err == nil {
		b.err = err
	}
}

// ToSQL compiles the builder into SQL. Mirrors Builder::toSql().
func (b *Builder) ToSQL() string {
	if b.grammar == nil {
		return ""
	}
	b.ApplyBeforeQueryCallbacks()
	return b.grammar.CompileSelect(b)
}

// BeforeQuery registers a closure to be invoked before the query is compiled. The closure receives
// the builder so it can add clauses. Mirrors Builder::beforeQuery().
func (b *Builder) BeforeQuery(callback func(*Builder) *Builder) *Builder {
	b.beforeQueryCallbacks = append(b.beforeQueryCallbacks, callback)
	return b
}

// ApplyBeforeQueryCallbacks invokes the registered before-query closures once and clears the queue.
// Mirrors Builder::applyBeforeQueryCallbacks().
func (b *Builder) ApplyBeforeQueryCallbacks() {
	for _, callback := range b.beforeQueryCallbacks {
		if callback != nil {
			callback(b)
		}
	}
	b.beforeQueryCallbacks = nil
}

// When runs callback when value is (or resolves to) truthy, and defaultCallback when value is falsy
// and a default was given. Mirrors Conditionable::when(), which the query Builder consumes through
// Database\Concerns\BuildsQueries. A func(*Builder) any value is resolved with the builder first,
// exactly like Laravel's `$value instanceof Closure ? $value($this) : $value`.
//
// The callback receives the builder and the resolved value; a non-nil result becomes the return value
// (Laravel's `return $callback(...) ?? $this`), while a nil result keeps the receiver, so the fluent
// chain is never broken. Laravel's higher-order proxy form — when() with no callback, or with a lone
// value — has no Go analogue and is not offered; callers pass the callback directly.
func (b *Builder) When(value any, callback func(*Builder, any) *Builder, defaultCallback ...func(*Builder, any) *Builder) *Builder {
	if resolver, ok := value.(func(*Builder) any); ok {
		value = resolver(b)
	}

	if truthy(value) {
		if callback != nil {
			if result := callback(b, value); result != nil {
				return result
			}
		}
		return b
	}

	if len(defaultCallback) > 0 && defaultCallback[0] != nil {
		if result := defaultCallback[0](b, value); result != nil {
			return result
		}
	}

	return b
}

// Unless runs callback when value is (or resolves to) falsy, and defaultCallback when value is truthy
// and a default was given. Mirrors Conditionable::unless(), the negation of when(): Laravel resolves a
// Closure value the same way, then takes the `if (! $value)` branch in place of `if ($value)`. Like
// When, the callback receives the builder and the resolved value, a non-nil result becomes the return
// value (Laravel's `return $callback(...) ?? $this`) and a nil result keeps the receiver. The
// higher-order proxy form is not offered for the same reason as When's.
func (b *Builder) Unless(value any, callback func(*Builder, any) *Builder, defaultCallback ...func(*Builder, any) *Builder) *Builder {
	if resolver, ok := value.(func(*Builder) any); ok {
		value = resolver(b)
	}

	if !truthy(value) {
		if callback != nil {
			if result := callback(b, value); result != nil {
				return result
			}
		}
		return b
	}

	if len(defaultCallback) > 0 && defaultCallback[0] != nil {
		if result := defaultCallback[0](b, value); result != nil {
			return result
		}
	}

	return b
}

// Tap passes the builder to the given callback and returns the receiver unchanged, mirroring
// BuildsQueries::tap(). The callback's return value is ignored; nil callbacks are a no-op so the
// fluent chain is never broken.
func (b *Builder) Tap(callback func(*Builder)) *Builder {
	if callback != nil {
		callback(b)
	}

	return b
}

// Pipe passes the builder to the given callback and returns the callback's result, mirroring
// BuildsQueries::pipe() (return $callback($this) ?? $this). When the callback result is nil - the Go
// spelling of PHP's null and of a void callback - the receiver is returned instead, so the fluent
// chain is never broken. A nil callback is likewise a no-op returning the receiver.
func (b *Builder) Pipe(callback func(*Builder) any) any {
	if callback != nil {
		if result := callback(b); result != nil {
			return result
		}
	}

	return b
}

// IsJoinClause reports whether this builder is a JoinClause.
func (b *Builder) IsJoinClause() bool { return b.isJoinClause }

// Bindings returns the raw binding map.
func (b *Builder) Bindings() map[string][]any { return b.bindings }

// RawBindings returns the bindings for one component.
func (b *Builder) RawBindings(typ string) []any { return b.bindings[typ] }

// GetBindings flattens the bindings in Laravel's component order. Mirrors Builder::getBindings(),
// which is Arr::flatten($this->bindings): a binding that is itself a list (for example a whereIn
// value list built from nested single-element arrays) is flattened into individual bindings.
func (b *Builder) GetBindings() []any {
	var out []any
	for _, t := range BindingTypes {
		out = append(out, flatten(b.bindings[t], 0)...)
	}
	return out
}

// SetBindings replaces the bindings for one component. Mirrors Builder::setBindings(), which stores
// the values as given: Laravel casts a binding only in addBinding and cleanBindings, so an enum set
// here is not unwrapped. The values are copied so the builder does not alias the caller's slice.
func (b *Builder) SetBindings(values []any, typ string) error {
	if _, ok := b.bindings[typ]; !ok {
		return errors.QueryInvalidBindingType.Args(typ)
	}
	stored := make([]any, len(values))
	copy(stored, values)
	b.bindings[typ] = stored
	return nil
}

// AddBinding appends a binding for one component. Mirrors Builder::addBinding().
func (b *Builder) AddBinding(value any, typ string) error {
	if _, ok := b.bindings[typ]; !ok {
		return errors.QueryInvalidBindingType.Args(typ)
	}
	if slice, ok := value.([]any); ok {
		merged := append(append([]any{}, b.bindings[typ]...), slice...)
		for i := range merged {
			merged[i] = CastBinding(merged[i])
		}
		b.bindings[typ] = merged
		return nil
	}
	b.bindings[typ] = append(b.bindings[typ], CastBinding(value))
	return nil
}

// MergeBindings merges another builder's bindings. Mirrors Builder::mergeBindings().
func (b *Builder) MergeBindings(other *Builder) {
	for _, t := range BindingTypes {
		_ = b.AddBinding(other.bindings[t], t)
	}
}

// MergeWheres merges an array of where clauses and bindings. Mirrors Builder::mergeWheres(): the
// incoming clauses are appended to this builder's, and the incoming bindings are appended to the
// "where" component in order - array_values(array_merge(...)) in Laravel, which a slice append
// reproduces without the PHP array keys. Laravel stores the merged bindings as given, without
// castBinding (which runs only in addBinding and cleanBindings), and so does this.
func (b *Builder) MergeWheres(wheres []Where, bindings []any) *Builder {
	b.wheres = append(b.wheres, wheres...)
	b.bindings["where"] = append(append([]any{}, b.bindings["where"]...), bindings...)

	return b
}

// UnitEnum is implemented by an enum type whose cases are identified by name: the Go counterpart of a
// PHP pure enum (UnitEnum). Go has no enum construct, so a type opts in by implementing EnumName, and
// CastBinding then binds a case as its name.
type UnitEnum interface {
	EnumName() string
}

// BackedEnum is implemented by an enum type whose cases carry a scalar backing value: the Go
// counterpart of PHP's BackedEnum. CastBinding binds a case as that value. A type implementing both
// interfaces binds its value, because PHP's BackedEnum extends UnitEnum and enum_value() tests it first.
type BackedEnum interface {
	EnumValue() any
}

// CastBinding mirrors Builder::castBinding(), which passes an enum case through enum_value(): a
// BackedEnum binds as its backing value and a UnitEnum as its name. Any other value is returned
// unchanged, so it keeps its dynamic type and database/sql converts it at execution.
//
// A nil pointer is also returned unchanged, even when its type is an enum. Calling a value-receiver
// method through a nil pointer panics, and database/sql binds a nil pointer as NULL, which matches
// castBinding(null) returning null.
func CastBinding(value any) any {
	switch enum := value.(type) {
	case BackedEnum:
		if !isNilPointer(enum) {
			return enum.EnumValue()
		}
	case UnitEnum:
		if !isNilPointer(enum) {
			return enum.EnumName()
		}
	}

	return value
}

// isNilPointer reports whether value holds a nil pointer.
func isNilPointer(value any) bool {
	rv := reflect.ValueOf(value)

	return rv.Kind() == reflect.Pointer && rv.IsNil()
}

// CleanBindings drops expressions and casts the rest. Mirrors Builder::cleanBindings().
func CleanBindings(values []any) []any {
	out := make([]any, 0, len(values))
	for _, v := range values {
		if IsExpression(v) {
			continue
		}
		out = append(out, CastBinding(v))
	}
	return out
}

// FlattenValue collapses a slice to its first scalar. Mirrors Builder::flattenValue().
func FlattenValue(value any) any {
	if slice, ok := value.([]any); ok {
		if len(slice) == 0 {
			return nil
		}
		return FlattenValue(slice[0])
	}
	return value
}

// --- cloning ----------------------------------------------------------------

// Clone returns a deep-enough copy. Laravel's PHP clone copies arrays by value; in Go a struct copy
// shares slice backing arrays, so every slice is copied explicitly.
func (b *Builder) Clone() *Builder {
	c := *b
	c.bindings = make(map[string][]any, len(b.bindings))
	for k, v := range b.bindings {
		c.bindings[k] = cloneSlice(v)
	}
	c.columns = cloneSlice(b.columns)
	c.distinctColumns = cloneSlice(b.distinctColumns)
	c.joins = cloneSlice(b.joins)
	c.wheres = cloneSlice(b.wheres)
	c.groups = cloneSlice(b.groups)
	c.havings = cloneSlice(b.havings)
	c.orders = cloneSlice(b.orders)
	c.unions = cloneSlice(b.unions)
	c.unionOrders = cloneSlice(b.unionOrders)
	c.beforeQueryCallbacks = cloneSlice(b.beforeQueryCallbacks)
	c.insertUsingColumns = cloneSlice(b.insertUsingColumns)
	c.insertUsingBindings = cloneSlice(b.insertUsingBindings)
	c.updateValues = cloneSlice(b.updateValues)
	if b.aggregate != nil {
		a := *b.aggregate
		a.Columns = cloneSlice(b.aggregate.Columns)
		c.aggregate = &a
	}
	if b.limit != nil {
		v := *b.limit
		c.limit = &v
	}
	if b.offset != nil {
		v := *b.offset
		c.offset = &v
	}
	if b.unionLimit != nil {
		v := *b.unionLimit
		c.unionLimit = &v
	}
	if b.unionOffset != nil {
		v := *b.unionOffset
		c.unionOffset = &v
	}
	return &c
}

// CloneWithout clones the builder with the named components cleared.
// Mirrors Builder::cloneWithout().
func (b *Builder) CloneWithout(components ...string) *Builder {
	c := b.Clone()
	for _, name := range components {
		switch name {
		case "aggregate":
			c.aggregate = nil
		case "columns":
			c.columns = nil
		case "from":
			c.from = nil
		case "joins":
			c.joins = nil
		case "wheres":
			c.wheres = nil
		case "groups":
			c.groups = nil
		case "havings":
			c.havings = nil
		case "orders":
			c.orders = nil
		case "limit":
			c.limit = nil
		case "offset":
			c.offset = nil
		case "unions":
			c.unions = nil
		case "unionLimit":
			c.unionLimit = nil
		case "unionOffset":
			c.unionOffset = nil
		case "unionOrders":
			c.unionOrders = nil
		case "lock":
			c.lock = nil
		}
	}
	return c
}

// CloneWithoutBindings clones the builder with the named binding components emptied.
// Mirrors Builder::cloneWithoutBindings().
func (b *Builder) CloneWithoutBindings(except ...string) *Builder {
	c := b.Clone()
	for _, t := range except {
		if _, ok := c.bindings[t]; ok {
			c.bindings[t] = []any{}
		}
	}
	return c
}

// --- where helpers ----------------------------------------------------------

// IsQueryable reports whether value can act as a subquery. Go has no Closure, so the builder
// accepts a builder or a callback that configures one.
// Mirrors Builder::isQueryable().
func IsQueryable(value any) bool {
	switch value.(type) {
	case *Builder, func(*Builder) *Builder:
		return true
	default:
		return false
	}
}

// PrepareValueAndOperator mirrors Builder::prepareValueAndOperator().
func (b *Builder) PrepareValueAndOperator(value, operator any, useDefault bool) (any, any, error) {
	if useDefault {
		return operator, "=", nil
	}
	if b.InvalidOperatorAndValue(operator, value) {
		return nil, nil, errors.QueryIllegalOperatorAndValue
	}
	return value, operator, nil
}

// InvalidOperatorAndValue mirrors Builder::invalidOperatorAndValue().
//
// The membership test is case-sensitive, as Laravel's in_array() is: "LIKE" with a null value is
// not an illegal combination, so it falls through to whereNull and renders "is not null", while
// lowercase "like" is refused. InvalidOperator lowercases separately, so both spellings still
// pass operator validation; only this null-value guard distinguishes them.
func (b *Builder) InvalidOperatorAndValue(operator, value any) bool {
	if value != nil {
		return false
	}
	op, ok := operator.(string)
	if !ok {
		return false
	}
	if !inStrings(op, operators) {
		return false
	}
	return !inStrings(op, []string{"=", "<=>", "<>", "!="})
}

// InvalidOperator mirrors Builder::invalidOperator().
func (b *Builder) InvalidOperator(operator any) bool {
	op, ok := operator.(string)
	if !ok {
		return true
	}
	lower := strings.ToLower(op)
	if inStrings(lower, operators) {
		return false
	}
	if b.grammar != nil && inStrings(lower, b.grammar.GetOperators()) {
		return false
	}
	return true
}

// IsBitwiseOperator mirrors Builder::isBitwiseOperator(): an operator is bitwise when it is in
// the base list or in whatever set the dialect declares through GetBitwiseOperators(). The base
// declares none, so only the dialect grammars add to the shared list.
func (b *Builder) IsBitwiseOperator(operator string) bool {
	lower := strings.ToLower(operator)
	if inStrings(lower, bitwiseOperators) {
		return true
	}

	return b.grammar != nil && inStrings(lower, b.grammar.GetBitwiseOperators())
}

// bitwiseOperators is the base bitwise operator list, copied from Builder::$bitwiseOperators.
var bitwiseOperators = []string{"&", "|", "^", "<<", ">>", "&~"}

// operators is the base operator list, copied verbatim from Illuminate\Database\Query\Builder.
var operators = []string{
	"=", "<", ">", "<=", ">=", "<>", "!=", "<=>",
	"like", "like binary", "not like", "ilike", "&", "|", "^", "<<", ">>",
	"&~", "is", "is not", "rlike", "not rlike", "regexp", "not regexp",
	"~", "~*", "!~", "!~*", "similar to", "not similar to", "not ilike",
	"~~*", "!~~*",
}

func inStrings(needle string, haystack []string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// cloneSlice copies a slice while preserving nil. Cloning with append([]T{}, nil...) would turn a
// nil slice into an empty non-nil one, which breaks the presence checks that drive compilation:
// a nil column list means "select *", and an empty non-nil one would emit no columns at all.
func cloneSlice[T any](in []T) []T {
	if in == nil {
		return nil
	}

	return append([]T(nil), in...)
}
