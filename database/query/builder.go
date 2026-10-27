package query

import (
	"reflect"
	"strings"

	"github.com/gradii/framework/database/query/core"
	"github.com/gradii/framework/errors"
)

// Builder is the dialect-neutral query builder. Its state mirrors
// Illuminate\Database\Query\Builder: flat clause descriptors plus positional bindings.
//
// Presence follows Laravel's isset($query->$component): a nil slice or nil pointer means the
// component is unset, so the grammar skips it.
type Builder struct {
	afterQueryCallbacks []func(any) any
	grammar             Grammar
	processor           Processor
	driver              string
	state               *State

	// err carries the first failure. Fluent methods return the builder, so errors cannot be
	// returned directly; this mirrors the existing database/db.Query builder.
	err error

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
	updateValues core.UpdateValues
}

// NewBuilder binds SQL compilation and result processing. A SQL-only builder may omit the
// processor; connections supply their dialect processor when the builder will execute inserts.
func NewBuilder(grammar Grammar, postProcessor ...Processor) *Builder {
	processor := NewProcessor("")
	if len(postProcessor) > 0 && postProcessor[0] != nil {
		processor = postProcessor[0]
	}
	b := &Builder{grammar: grammar, processor: processor, state: &State{Bindings: make(map[string][]any, len(BindingTypes))}}
	for _, t := range BindingTypes {
		b.State().Bindings[t] = []any{}
	}
	return b
}

// NewQuery mirrors Builder::newQuery(): a fresh builder sharing the grammar, processor, and dialect.
func (b *Builder) NewQuery() *Builder {
	n := NewBuilder(b.grammar, b.processor)
	n.driver = b.driver
	n.State().UseUpsertAlias = b.State().UseUpsertAlias
	return n
}

// GroupLimit adds a "group limit" clause to the query, used to cap the rows per group. Mirrors
// Builder::groupLimit(): a negative value is ignored and leaves any previously set group limit
// untouched, and the presence flag is what compileSelect tests (Laravel's isset($query->groupLimit)).
func (b *Builder) GroupLimit(value int, column string) *Builder {
	if value >= 0 {
		b.State().GroupLimit = value
		b.State().GroupLimitColumn = column
		b.State().GroupLimitSet = true
	}

	return b
}

// SetDriver records the dialect name, used to convert placeholders at execution time.
func (b *Builder) SetDriver(driver string) { b.driver = driver }

// SetUseUpsertAlias records the connection's use_upsert_alias setting on the builder. Laravel's
// MySQL grammar reads it from the connection in compileUpsert; Go carries it on the builder, which
// represents $query, and the MySQL grammar reads it there.
func (b *Builder) SetUseUpsertAlias(alias bool) { b.State().UseUpsertAlias = alias }

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
func (b *Builder) IsJoinClause() bool { return b.State().IsJoinClause }

// Bindings returns the raw binding map.
func (b *Builder) Bindings() map[string][]any { return b.State().Bindings }

// RawBindings returns the bindings for one component.
func (b *Builder) RawBindings(typ string) []any { return b.State().Bindings[typ] }

// GetBindings flattens the bindings in Laravel's component order. Mirrors Builder::getBindings(),
// which is Arr::flatten($this->bindings): a binding that is itself a list (for example a whereIn
// value list built from nested single-element arrays) is flattened into individual bindings.
func (b *Builder) GetBindings() []any {
	var out []any
	for _, t := range BindingTypes {
		out = append(out, flatten(b.State().Bindings[t], 0)...)
	}
	return out
}

// SetBindings replaces the bindings for one component. Mirrors Builder::setBindings(), which stores
// the values as given: Laravel casts a binding only in addBinding and cleanBindings, so an enum set
// here is not unwrapped. The values are copied so the builder does not alias the caller's slice.
func (b *Builder) SetBindings(values []any, typ string) error {
	if _, ok := b.State().Bindings[typ]; !ok {
		return errors.QueryInvalidBindingType.Args(typ)
	}
	stored := make([]any, len(values))
	copy(stored, values)
	b.State().Bindings[typ] = stored
	return nil
}

// AddBinding appends a binding for one component. Mirrors Builder::addBinding().
func (b *Builder) AddBinding(value any, typ string) error {
	if _, ok := b.State().Bindings[typ]; !ok {
		return errors.QueryInvalidBindingType.Args(typ)
	}
	if slice, ok := value.([]any); ok {
		merged := append(append([]any{}, b.State().Bindings[typ]...), slice...)
		for i := range merged {
			merged[i] = CastBinding(merged[i])
		}
		b.State().Bindings[typ] = merged
		return nil
	}
	b.State().Bindings[typ] = append(b.State().Bindings[typ], CastBinding(value))
	return nil
}

// MergeBindings merges another builder's bindings. Mirrors Builder::mergeBindings().
func (b *Builder) MergeBindings(other *Builder) {
	for _, t := range BindingTypes {
		_ = b.AddBinding(other.State().Bindings[t], t)
	}
}

// MergeWheres merges an array of where clauses and bindings. Mirrors Builder::mergeWheres(): the
// incoming clauses are appended to this builder's, and the incoming bindings are appended to the
// "where" component in order - array_values(array_merge(...)) in Laravel, which a slice append
// reproduces without the PHP array keys. Laravel stores the merged bindings as given, without
// castBinding (which runs only in addBinding and cleanBindings), and so does this.
func (b *Builder) MergeWheres(wheres []core.Where, bindings []any) *Builder {
	b.State().Wheres = append(b.State().Wheres, wheres...)
	b.State().Bindings["where"] = append(append([]any{}, b.State().Bindings["where"]...), bindings...)

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
	state := *b.State()
	c.state = &state
	c.State().Bindings = make(map[string][]any, len(b.State().Bindings))
	for k, v := range b.State().Bindings {
		c.State().Bindings[k] = cloneSlice(v)
	}
	c.State().Columns = cloneSlice(b.State().Columns)
	c.State().DistinctColumns = cloneSlice(b.State().DistinctColumns)
	c.State().Joins = cloneSlice(b.State().Joins)
	c.State().Wheres = cloneSlice(b.State().Wheres)
	c.State().Groups = cloneSlice(b.State().Groups)
	c.State().Havings = cloneSlice(b.State().Havings)
	c.State().Orders = cloneSlice(b.State().Orders)
	c.State().Unions = cloneSlice(b.State().Unions)
	c.State().UnionOrders = cloneSlice(b.State().UnionOrders)
	c.beforeQueryCallbacks = cloneSlice(b.beforeQueryCallbacks)
	c.afterQueryCallbacks = cloneSlice(b.afterQueryCallbacks)
	c.insertUsingColumns = cloneSlice(b.insertUsingColumns)
	c.insertUsingBindings = cloneSlice(b.insertUsingBindings)
	c.updateValues = cloneSlice(b.updateValues)
	if b.State().Aggregate != nil {
		a := *b.State().Aggregate
		a.Columns = cloneSlice(b.State().Aggregate.Columns)
		c.State().Aggregate = &a
	}
	if b.State().Limit != nil {
		v := *b.State().Limit
		c.State().Limit = &v
	}
	if b.State().Offset != nil {
		v := *b.State().Offset
		c.State().Offset = &v
	}
	if b.State().UnionLimit != nil {
		v := *b.State().UnionLimit
		c.State().UnionLimit = &v
	}
	if b.State().UnionOffset != nil {
		v := *b.State().UnionOffset
		c.State().UnionOffset = &v
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
			c.State().Aggregate = nil
		case "columns":
			c.State().Columns = nil
		case "from":
			c.State().From = nil
		case "joins":
			c.State().Joins = nil
		case "wheres":
			c.State().Wheres = nil
		case "groups":
			c.State().Groups = nil
		case "havings":
			c.State().Havings = nil
		case "orders":
			c.State().Orders = nil
		case "limit":
			c.State().Limit = nil
		case "offset":
			c.State().Offset = nil
		case "unions":
			c.State().Unions = nil
		case "unionLimit":
			c.State().UnionLimit = nil
		case "unionOffset":
			c.State().UnionOffset = nil
		case "unionOrders":
			c.State().UnionOrders = nil
		case "lock":
			c.State().Lock = nil
		}
	}
	return c
}

// CloneWithoutBindings clones the builder with the named binding components emptied.
// Mirrors Builder::cloneWithoutBindings().
func (b *Builder) CloneWithoutBindings(except ...string) *Builder {
	c := b.Clone()
	for _, t := range except {
		if _, ok := c.State().Bindings[t]; ok {
			c.State().Bindings[t] = []any{}
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

// State exposes the compilation clauses to the grammar.
func (b *Builder) State() *State {
	if b.state == nil {
		b.state = &State{}
	}
	return b.state
}

// CloneForGrammar lets compilers rewrite a copy without depending on the concrete builder.
func (b *Builder) CloneForGrammar() GrammarQuery { return b.Clone() }

// RebindGrammar returns a copy carrying the builder's clauses but compiling them with another
// grammar, processor and dialect. The clauses are recorded dialect-neutral and are rendered only at
// compile time, so a query moved onto a different connection must keep its clauses and change the
// grammar with them: hand a MySQL-built builder to Postgres and it renders backticks, hand it a
// processor that expects no RETURNING and the insert loses its key. Laravel has the same split — the
// clauses live on Query\Builder while the grammar and processor come from the connection it is
// attached to.
func (b *Builder) RebindGrammar(grammar Grammar, processor Processor, driver string) *Builder {
	c := b.Clone()
	c.grammar = grammar
	if processor != nil {
		c.processor = processor
	}
	c.driver = driver

	return c
}

// SetError records a compilation error while preserving the first failure.
func (b *Builder) SetError(err error) { b.setError(err) }
