package relations

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbcontract "github.com/gradii/framework/contracts/database/db"
	contractsorm "github.com/gradii/framework/contracts/database/orm"
)

// --- stubs -----------------------------------------------------------------------------------------

// recordingBuilder is the Builder stand-in for the object layer's terminal paths. It embeds the
// Builder interface (left nil) so only the methods the base semantics actually reach are
// overridden, and every call is recorded in order together with the argument that matters.
type recordingBuilder struct {
	Builder

	calls []string
	args  []any

	existsResult bool
	existsErr    error
	getErr       error
}

func (b *recordingBuilder) record(name string, arg any) {
	b.calls = append(b.calls, name)
	b.args = append(b.args, arg)
}

func (b *recordingBuilder) Clone() contractsorm.Query {
	b.record("Clone", nil)

	return &recordingQuery{parent: b}
}

func (b *recordingBuilder) Get(dest any, conds ...any) error {
	b.record("Get", dest)
	if len(conds) > 0 {
		b.record("Get.conds", conds)
	}

	return b.getErr
}

func (b *recordingBuilder) First(dest any) error {
	b.record("First", dest)

	return b.getErr
}

func (b *recordingBuilder) WhereKey(id any) contractsorm.Query {
	b.record("WhereKey", id)

	return &recordingQuery{parent: b}
}

func (b *recordingBuilder) Exists() (bool, error) {
	b.record("Exists", nil)

	return b.existsResult, b.existsErr
}

// recordingQuery is the handle a builder method returned: the write chain continues on it and its
// statements land in the same record.
type recordingQuery struct {
	contractsorm.Query

	parent *recordingBuilder
}

func (q *recordingQuery) WithoutGlobalScopes(names ...string) contractsorm.Query {
	q.parent.record("WithoutGlobalScopes", names)

	return q
}

func (q *recordingQuery) Update(column any, value ...any) (*dbcontract.Result, error) {
	q.parent.record("Update", column)
	if len(value) > 0 {
		q.parent.record("Update.value", value[0])
	}

	return &dbcontract.Result{RowsAffected: 1}, nil
}

func (q *recordingQuery) WhereKey(id any) contractsorm.Query {
	q.parent.record("WhereKey", id)

	return q
}

func (q *recordingQuery) Exists() (bool, error) {
	q.parent.record("Exists", nil)

	return q.parent.existsResult, q.parent.existsErr
}

// mapModel is the shape the hook-driven tests compare: columns as plain values plus the
// pseudo-columns __table, __connection and __key the table / connection / key hooks read.
type mapModel map[string]any

// mapHooks reads the column values off mapModel fixtures and records every hook call in order.
func mapHooks(log *[]string) Hooks {
	note := func(entry string) {
		if log != nil {
			*log = append(*log, entry)
		}
	}
	read := func(model any, column string) (any, error) {
		values, ok := model.(mapModel)
		if !ok {
			return nil, errors.New("not a map model")
		}

		return values[column], nil
	}

	return Hooks{
		ReadColumn: func(model any, column string) (any, error) {
			note("read:" + column)

			return read(model, column)
		},
		ReadModelColumn: func(model any, column string) (any, error) {
			note("readModel:" + column)

			return read(model, column)
		},
		ModelTable: func(model any) string {
			note("table")
			values, ok := model.(mapModel)
			if !ok {
				return ""
			}
			table, _ := values["__table"].(string)

			return table
		},
		ModelConnection: func(model any) string {
			note("connection")
			values, ok := model.(mapModel)
			if !ok {
				return ""
			}
			connection, _ := values["__connection"].(string)

			return connection
		},
		ModelKey: func(model any) any {
			note("key")
			values, ok := model.(mapModel)
			if !ok {
				return nil
			}

			return values["__key"]
		},
	}
}

// baseHooks is mapHooks plus the read / default hooks the GetResults paths use.
func baseHooks(log *[]string, found bool, build func() (any, bool, error)) Hooks {
	hooks := mapHooks(log)
	hooks.FirstRow = func(contractsorm.Query, any) (bool, error) {
		if log != nil {
			*log = append(*log, "firstRow")
		}

		return found, nil
	}
	hooks.BuildDefault = func(value any, parent any) (any, bool, error) {
		if log != nil {
			*log = append(*log, "default")
		}
		if build == nil {
			return nil, false, nil
		}

		return build()
	}

	return hooks
}

// --- compareKeys -----------------------------------------------------------------------------------

func TestCompareRelationKeys_Matrix(t *testing.T) {
	seven := uint64(7)

	cases := []struct {
		name    string
		parent  any
		related any
		want    bool
	}{
		// Pinned by reflection against illuminate/database v12.55.1's ComparesRelatedModels::compareKeys.
		{"int 1 vs int 1", 1, 1, true},
		{"int 1 vs string 1", 1, "1", true},
		{"string 1 vs int 1", "1", 1, true},
		{"int 1 vs int 2", 1, 2, false},
		{"int 1 vs string 1.5", 1, "1.5", true},
		{"int 1 vs string 1abc", 1, "1abc", true},
		{"int 1 vs float 1.0", 1, 1.0, true},
		{"int 1 vs float 1.5", 1, 1.5, true},
		{"string 1 vs string 1", "1", "1", true},
		{"string 1 vs string 1.5", "1", "1.5", false},
		{"string 1.5 vs string 1", "1.5", "1", false},
		{"string abc vs string abc", "abc", "abc", true},
		{"string abc vs string ABC", "abc", "ABC", false},
		{"string abc vs int 1", "abc", 1, false},
		{"int 1 vs string abc", 1, "abc", false},
		{"int 0 vs int 0", 0, 0, false},
		{"string 0 vs string 0", "0", "0", false},
		{"int 1 vs string 01", 1, "01", true},
		{"int 1 vs string space 1", 1, " 1", true},
		{"int 1 vs string 1.9", 1, "1.9", true},
		{"int 1 vs empty string", 1, "", false},
		{"empty string vs empty string", "", "", false},
		{"nil vs int 1", nil, 1, false},
		{"int 1 vs nil", 1, nil, false},
		{"int 1 vs true", 1, true, true},
		{"false vs string 0", false, "0", false},
		{"float 1.5 vs float 1.5", 1.5, 1.5, true},
		{"float 1.5 vs string 1.5", 1.5, "1.5", false},
		{"uint8 7 vs int64 7", uint8(7), int64(7), true},
		{"nil pointer vs int 1", (*uint64)(nil), 1, false},
		{"pointer to 7 vs uint64 7", &seven, uint64(7), true},
		{"string 1 vs string 01 stays strict", "1", "01", false},
		{"string 01 vs string 1 stays strict", "01", "1", false},
		// PHP casts a pure integer run with strtoll: exact across the full 64-bit range and
		// saturating at the bounds, while float64 routing rounds past 2^53 — pinned by executing
		// the oracle: (int) '9007199254740993' keeps its last digit, (int) '9223372036854775807999'
		// saturates to PHP_INT_MAX, (int) '-9223372036854775807999' to PHP_INT_MIN, and (int) 1e20
		// wraps modulo 2^64 to 7766279631452241920.
		{"int64 2^53+1 vs its string", int64(9007199254740993), "9007199254740993", true},
		{"maxint64 vs oversized string saturates equal", int64(9223372036854775807), "9223372036854775807999", true},
		{"minint64 vs undersized string saturates equal", int64(-9223372036854775808), "-9223372036854775807999", true},
		{"float 1e20 wraps modulo 2^64", 1e20, int64(7766279631452241920), true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, compareRelationKeys(tc.parent, tc.related))
		})
	}
}

// --- Is -------------------------------------------------------------------------------------------

func TestRelationQuery_Is_PerKind(t *testing.T) {
	hasOne := RelationInfo{Kind: KindHasOne, RelatedTable: "books", References: []RefKey{{PrimaryColumn: "id", ForeignColumn: "user_id"}}}
	morphOne := RelationInfo{Kind: KindMorphOne, RelatedTable: "images", References: []RefKey{{PrimaryColumn: "id", ForeignColumn: "imageable_id"}}}
	belongsTo := RelationInfo{Kind: KindBelongsTo, RelatedTable: "authors", References: []RefKey{{PrimaryColumn: "id", ForeignColumn: "author_id"}}}
	morphTo := RelationInfo{Kind: KindMorphTo, RelatedTable: "users", MorphIDColumn: "imageable_id", MorphOwnerKey: "id"}
	through := RelationInfo{Kind: KindHasOneThrough, RelatedTable: "posts", LocalKey: "id", SecondKey: "user_id"}

	cases := []struct {
		name   string
		info   RelationInfo
		parent mapModel
		model  mapModel
		want   bool
	}{
		{"has-one equal keys", hasOne, mapModel{"id": 7}, mapModel{"user_id": uint64(7), "__table": "books"}, true},
		{"has-one int vs string cast", hasOne, mapModel{"id": 7}, mapModel{"user_id": "7", "__table": "books"}, true},
		{"has-one string vs int cast", hasOne, mapModel{"id": "7"}, mapModel{"user_id": 7, "__table": "books"}, true},
		{"has-one different key", hasOne, mapModel{"id": 7}, mapModel{"user_id": 8, "__table": "books"}, false},
		{"has-one other table", hasOne, mapModel{"id": 7}, mapModel{"user_id": 7, "__table": "posts"}, false},
		{"has-one other connection", RelationInfo{Kind: KindHasOne, RelatedTable: "books", RelatedConnection: "mysql", References: hasOne.References}, mapModel{"id": 7}, mapModel{"user_id": 7, "__table": "books", "__connection": "pgsql"}, false},
		{"has-one same connection", RelationInfo{Kind: KindHasOne, RelatedTable: "books", RelatedConnection: "mysql", References: hasOne.References}, mapModel{"id": 7}, mapModel{"user_id": 7, "__table": "books", "__connection": "mysql"}, true},
		{"has-one empty model key", hasOne, mapModel{"id": 7}, mapModel{"user_id": 0, "__table": "books"}, false},
		{"has-one empty parent key", hasOne, mapModel{"id": 0}, mapModel{"user_id": 7, "__table": "books"}, false},
		{"has-one string zero vs string zero", hasOne, mapModel{"id": "0"}, mapModel{"user_id": "0", "__table": "books"}, false},
		{"morph-one keys", morphOne, mapModel{"id": 9}, mapModel{"imageable_id": 9, "__table": "images"}, true},
		{"morph-one cast", morphOne, mapModel{"id": 9}, mapModel{"imageable_id": "9", "__table": "images"}, true},
		{"morph-one different key", morphOne, mapModel{"id": 9}, mapModel{"imageable_id": 2, "__table": "images"}, false},
		{"belongs-to keys", belongsTo, mapModel{"author_id": 5}, mapModel{"id": "5", "__table": "authors"}, true},
		{"belongs-to nil fk", belongsTo, mapModel{"author_id": nil}, mapModel{"id": 5, "__table": "authors"}, false},
		{"belongs-to zero fk", belongsTo, mapModel{"author_id": uint64(0)}, mapModel{"id": 0, "__table": "authors"}, false},
		{"morph-to keys", morphTo, mapModel{"imageable_id": 3}, mapModel{"id": 3, "__table": "users"}, true},
		{"morph-to cast", morphTo, mapModel{"imageable_id": 3}, mapModel{"id": "3", "__table": "users"}, true},
		{"morph-to other key", morphTo, mapModel{"imageable_id": 3}, mapModel{"id": 4, "__table": "users"}, false},
		{"has-one-through keys", through, mapModel{"id": 1}, mapModel{"user_id": 1, "__table": "posts"}, true},
		{"has-one-through other key", through, mapModel{"id": 1}, mapModel{"user_id": 2, "__table": "posts"}, false},
		{"many2many not expressible", RelationInfo{Kind: KindMany2Many, RelatedTable: "roles"}, mapModel{"id": 1}, mapModel{"id": 1, "__table": "roles"}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rel := &relationQuery{info: tc.info, parent: tc.parent, hooks: mapHooks(nil)}
			assert.Equal(t, tc.want, rel.Is(tc.model))
			assert.Equal(t, !tc.want, rel.IsNot(tc.model))
		})
	}

	rel := &relationQuery{info: hasOne, parent: mapModel{"id": 7}, hooks: mapHooks(nil)}
	assert.False(t, rel.Is(nil), "an untyped nil never matches")
	assert.False(t, rel.Is((*mapModel)(nil)), "a nil pointer never matches")
}

func TestRelationQuery_Is_ConsultOrderAndPartialGuard(t *testing.T) {
	var log []string
	hooks := mapHooks(&log)
	info := RelationInfo{
		Kind:         KindHasOne,
		RelatedTable: "books",
		References:   []RefKey{{PrimaryColumn: "id", ForeignColumn: "user_id"}},
	}
	parent := mapModel{"id": 7}
	match := mapModel{"user_id": 7, "__table": "books"}

	// A nil model is rejected before any hook runs — not even the keys are read.
	log = nil
	rel := &relationQuery{info: info, parent: parent, hooks: hooks}
	assert.False(t, rel.Is(nil))
	assert.Empty(t, log)

	// A failing key comparison stops before the table and the connection are consulted.
	log = nil
	rel = &relationQuery{info: info, parent: parent, hooks: hooks}
	assert.False(t, rel.Is(mapModel{"user_id": 8, "__table": "books"}))
	assert.Equal(t, []string{"read:id", "readModel:user_id"}, log)

	// The keys are read off the parent and the model in that order, then table, connection, guard.
	log = nil
	oneOfMany := info
	oneOfMany.OneOfMany = true
	builder := &recordingBuilder{existsResult: true}
	rel = &relationQuery{Builder: builder, info: oneOfMany, parent: parent, hooks: hooks}
	assert.True(t, rel.Is(mapModel{"user_id": 7, "__table": "books", "__key": 7}))
	assert.Equal(t, []string{"read:id", "readModel:user_id", "table", "connection", "key"}, log)
	assert.Equal(t, []string{"WhereKey", "Exists"}, builder.calls)
	assert.Equal(t, []any{7, nil}, builder.args)

	// The guard's exists decides the result: one-of-many only "is" the row the aggregate selects.
	builder = &recordingBuilder{existsResult: false}
	rel = &relationQuery{Builder: builder, info: oneOfMany, parent: parent, hooks: hooks}
	assert.False(t, rel.Is(mapModel{"user_id": 7, "__table": "books", "__key": 7}))
	assert.Equal(t, []string{"WhereKey", "Exists"}, builder.calls)

	// A nil model key cannot satisfy the guard; no query runs on the builder.
	builder = &recordingBuilder{}
	rel = &relationQuery{Builder: builder, info: oneOfMany, parent: parent, hooks: hooks}
	assert.False(t, rel.Is(match))
	assert.Empty(t, builder.calls)

	// Without one-of-many the guard is skipped entirely.
	builder = &recordingBuilder{existsResult: false}
	rel = &relationQuery{Builder: builder, info: info, parent: parent, hooks: hooks}
	assert.True(t, rel.Is(match))
	assert.Empty(t, builder.calls)

	// Kinds without SupportsPartialRelations never run the guard.
	hasMany := info
	hasMany.Kind = KindHasMany
	hasMany.OneOfMany = true
	builder = &recordingBuilder{existsResult: false}
	rel = &relationQuery{Builder: builder, info: hasMany, parent: parent, hooks: hooks}
	assert.True(t, rel.Is(match))
	assert.Empty(t, builder.calls)

	// A table mismatch stops before the connection is consulted.
	log = nil
	rel = &relationQuery{info: info, parent: parent, hooks: hooks}
	assert.False(t, rel.Is(mapModel{"user_id": 7, "__table": "posts"}))
	assert.Equal(t, []string{"read:id", "readModel:user_id", "table"}, log)

	// A connection mismatch, keys and table equal, is no match.
	log = nil
	rel = &relationQuery{info: RelationInfo{Kind: KindHasOne, RelatedTable: "books", References: info.References}, parent: parent, hooks: hooks}
	assert.False(t, rel.Is(mapModel{"user_id": 7, "__table": "books", "__connection": "pgsql"}))
	assert.Equal(t, []string{"read:id", "readModel:user_id", "table", "connection"}, log)
}

// --- GetResults -----------------------------------------------------------------------------------

type defaultModel struct{ Name string }

type singleKindCase struct {
	name   string
	kind   Kind
	info   RelationInfo
	parent mapModel
	column string
}

func singleKindCases() []singleKindCase {
	return []singleKindCase{
		{"has-one", KindHasOne, RelationInfo{Kind: KindHasOne, References: []RefKey{{PrimaryColumn: "id", ForeignColumn: "user_id"}}}, mapModel{"id": uint64(7)}, "id"},
		{"morph-one", KindMorphOne, RelationInfo{Kind: KindMorphOne, References: []RefKey{{PrimaryColumn: "id", ForeignColumn: "imageable_id"}}}, mapModel{"id": uint64(7)}, "id"},
		{"belongs-to", KindBelongsTo, RelationInfo{Kind: KindBelongsTo, References: []RefKey{{PrimaryColumn: "id", ForeignColumn: "author_id"}}}, mapModel{"author_id": uint64(7)}, "author_id"},
		{"morph-to", KindMorphTo, RelationInfo{Kind: KindMorphTo, MorphIDColumn: "imageable_id"}, mapModel{"imageable_id": uint64(7)}, "imageable_id"},
		{"has-one-through", KindHasOneThrough, RelationInfo{Kind: KindHasOneThrough, LocalKey: "id", SecondKey: "user_id"}, mapModel{"id": uint64(7)}, "id"},
	}
}

func TestRelationQuery_GetResults_SingleKinds(t *testing.T) {
	build := func() (any, bool, error) { return &defaultModel{Name: "fallback"}, true, nil }

	for _, tc := range singleKindCases() {
		t.Run(tc.name+" miss materialises default", func(t *testing.T) {
			var log []string
			rel := &relationQuery{info: tc.info, parent: tc.parent, hooks: baseHooks(&log, false, build)}

			var dest defaultModel
			require.NoError(t, rel.GetResults(&dest))
			assert.Equal(t, "fallback", dest.Name)
			assert.Equal(t, []string{"read:" + tc.column, "firstRow", "default"}, log)
		})

		t.Run(tc.name+" found skips default", func(t *testing.T) {
			var log []string
			rel := &relationQuery{info: tc.info, parent: tc.parent, hooks: baseHooks(&log, true, build)}

			hit := defaultModel{Name: "row"}
			require.NoError(t, rel.GetResults(&hit))
			assert.Equal(t, "row", hit.Name)
			assert.Equal(t, []string{"read:" + tc.column, "firstRow"}, log)
		})

		// For the single-result kinds the absent parent key is the default's short-circuit: no
		// read runs at all (the many-result kinds instead execute the query — see
		// TestRelationQuery_GetResults_ManyKindsReadTheBuilder).
		for _, absence := range []struct {
			name  string
			value any
		}{
			{"nil", nil},
			{"nil pointer", (*uint64)(nil)},
			{"zero", uint64(0)},
			{"empty string", ""},
		} {
			t.Run(tc.name+" absent parent key ("+absence.name+")", func(t *testing.T) {
				var log []string
				parent := mapModel{tc.column: absence.value}
				rel := &relationQuery{info: tc.info, parent: parent, hooks: baseHooks(&log, true, build)}

				var dest defaultModel
				require.NoError(t, rel.GetResults(&dest))
				assert.Equal(t, "fallback", dest.Name)
				assert.Equal(t, []string{"read:" + tc.column, "default"}, log)
			})
		}

		t.Run(tc.name+" default disabled leaves dest untouched", func(t *testing.T) {
			var log []string
			hooks := baseHooks(&log, false, func() (any, bool, error) { return nil, false, nil })
			rel := &relationQuery{info: tc.info, parent: tc.parent, hooks: hooks}

			var dest defaultModel
			require.NoError(t, rel.GetResults(&dest))
			assert.Equal(t, defaultModel{}, dest)
		})

		t.Run(tc.name+" default builder error propagates", func(t *testing.T) {
			wantErr := errors.New("default failed")
			hooks := baseHooks(nil, false, func() (any, bool, error) { return nil, false, wantErr })
			rel := &relationQuery{info: tc.info, parent: tc.parent, hooks: hooks}

			var dest defaultModel
			assert.ErrorIs(t, rel.GetResults(&dest), wantErr)
		})
	}
}

// The many-kind half of the empty-key alignment: a many/through read never consults the parent key
// — no read runs, no default materialises — and the constrained query executes as usual. Oracle
// (illuminate/database v12.55.1, executed, /tmp/lalign-getresults/getresults.php): a zero
// (non-null) parent key runs the constraint query for every kind (hasMany queries=1); the
// strictly-null key Laravel short-circuits to an empty collection is unreachable for a value-typed
// Go key, whose never-set zero is the key a caller can express.
func TestRelationQuery_GetResults_ManyKindsReadTheBuilder(t *testing.T) {
	cases := []struct {
		kind Kind
		info RelationInfo
	}{
		{KindHasMany, RelationInfo{Kind: KindHasMany, References: []RefKey{{PrimaryColumn: "id", ForeignColumn: "user_id"}}}},
		{KindMorphMany, RelationInfo{Kind: KindMorphMany, References: []RefKey{{PrimaryColumn: "id", ForeignColumn: "imageable_id"}}}},
		{KindMany2Many, RelationInfo{Kind: KindMany2Many, PivotParentRef: RefKey{PrimaryColumn: "id", ForeignColumn: "user_id"}}},
		{KindMorphToMany, RelationInfo{Kind: KindMorphToMany, PivotParentRef: RefKey{PrimaryColumn: "id", ForeignColumn: "user_id"}}},
		{KindHasManyThrough, RelationInfo{Kind: KindHasManyThrough, LocalKey: "id"}},
	}

	for _, tc := range cases {
		t.Run("kind "+kindName(tc.kind), func(t *testing.T) {
			var log []string
			builder := &recordingBuilder{}
			// The full hook set over an empty parent key: a short-circuit reading the key or
			// materialising a default would land in the log — a many-kind read must leave it empty.
			hooks := baseHooks(&log, false, func() (any, bool, error) { return &defaultModel{Name: "fallback"}, true, nil })
			rel := &relationQuery{Builder: builder, info: tc.info, parent: mapModel{"id": uint64(0)}, hooks: hooks}

			dest := []defaultModel{}
			require.NoError(t, rel.GetResults(&dest))
			assert.Equal(t, []string{"Get"}, builder.calls)
			assert.Equal(t, []any{&dest}, builder.args)
			assert.Empty(t, log, "a many-kind read never reads the parent key nor materialises the default")
			assert.Empty(t, dest)
		})
	}
}

func kindName(kind Kind) string {
	switch kind {
	case KindHasOne:
		return "has-one"
	case KindHasMany:
		return "has-many"
	case KindBelongsTo:
		return "belongs-to"
	case KindMany2Many:
		return "many2many"
	case KindMorphOne:
		return "morph-one"
	case KindMorphMany:
		return "morph-many"
	case KindMorphTo:
		return "morph-to"
	case KindMorphToMany:
		return "morph-to-many"
	case KindHasOneThrough:
		return "has-one-through"
	case KindHasManyThrough:
		return "has-many-through"
	}

	return "unknown"
}

func TestRelationQuery_GetResults_NoReadHookFallsBackToFirst(t *testing.T) {
	// A shape without the parent read hook cannot evaluate the short-circuit; the read path still
	// runs, preserving the builder fallback.
	var log []string
	hooks := Hooks{
		FirstRow: func(contractsorm.Query, any) (bool, error) {
			log = append(log, "firstRow")

			return false, nil
		},
		BuildDefault: func(value any, parent any) (any, bool, error) {
			log = append(log, "default")

			return &defaultModel{Name: "fallback"}, true, nil
		},
	}
	rel := &relationQuery{info: RelationInfo{Kind: KindHasOne, References: []RefKey{{PrimaryColumn: "id", ForeignColumn: "user_id"}}}, parent: mapModel{"id": 7}, hooks: hooks}

	var dest defaultModel
	require.NoError(t, rel.GetResults(&dest))
	assert.Equal(t, "fallback", dest.Name)
	assert.Equal(t, []string{"firstRow", "default"}, log)
}

func TestRelationQuery_PromotedFirst_IgnoresDefault(t *testing.T) {
	var log []string
	builder := &recordingBuilder{}
	rel := Wrap(builder, mapModel{"id": 7}, "profile", RelationInfo{
		Kind:       KindHasOne,
		References: []RefKey{{PrimaryColumn: "id", ForeignColumn: "user_id"}},
	}, baseHooks(&log, false, func() (any, bool, error) { return &defaultModel{Name: "fallback"}, true, nil }))

	var dest defaultModel
	require.NoError(t, rel.First(&dest))
	assert.Equal(t, []string{"First"}, builder.calls)
	assert.Empty(t, log, "the builder path never reads the parent key nor materialises the default")
	assert.Equal(t, defaultModel{}, dest)
}

// --- Touch ----------------------------------------------------------------------------------------

func TestRelationQuery_Touch_ChainsCloneUnscopedUpdate(t *testing.T) {
	builder := &recordingBuilder{}
	rel := &relationQuery{Builder: builder, info: RelationInfo{Kind: KindHasOne}, hooks: Hooks{
		UpdatedAtColumn: func() (string, error) { return "updated_at", nil },
	}}

	before := time.Now()
	result, err := rel.Touch()
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.EqualValues(t, 1, result.RowsAffected)

	assert.Equal(t, []string{"Clone", "WithoutGlobalScopes", "Update"}, builder.calls)
	assert.Nil(t, builder.args[0])
	assert.Nil(t, builder.args[1], "Touch drops every global scope (no names)")
	updated, ok := builder.args[2].(map[string]any)
	require.True(t, ok)
	stamp, ok := updated["updated_at"].(time.Time)
	require.True(t, ok)
	assert.WithinDuration(t, before, stamp, time.Minute)
}

func TestRelationQuery_Touch_IgnoringRelatedStampsNothing(t *testing.T) {
	var log []string
	builder := &recordingBuilder{}
	rel := &relationQuery{Builder: builder, info: RelationInfo{Kind: KindHasMany}, hooks: Hooks{
		IgnoringTouch: func() bool {
			log = append(log, "ignoring")

			return true
		},
		UpdatedAtColumn: func() (string, error) {
			log = append(log, "column")

			return "updated_at", nil
		},
	}}

	result, err := rel.Touch()
	require.NoError(t, err)
	assert.EqualValues(t, 0, result.RowsAffected)
	assert.Equal(t, []string{"ignoring"}, log)
	assert.Empty(t, builder.calls)
}

func TestRelationQuery_Touch_ColumnlessModelStampsNothing(t *testing.T) {
	var log []string
	builder := &recordingBuilder{}
	rel := &relationQuery{Builder: builder, info: RelationInfo{Kind: KindHasMany}, hooks: Hooks{
		UpdatedAtColumn: func() (string, error) {
			log = append(log, "column")

			return "", nil
		},
	}}

	result, err := rel.Touch()
	require.NoError(t, err)
	assert.EqualValues(t, 0, result.RowsAffected)
	assert.Equal(t, []string{"column"}, log)
	assert.Empty(t, builder.calls)
}

func TestRelationQuery_Touch_BelongsToAbsentKeyIsANoOpBeforeAnyCheck(t *testing.T) {
	for _, absence := range []struct {
		name  string
		value any
	}{
		{"nil", nil},
		{"nil pointer", (*uint64)(nil)},
		{"zero", uint64(0)},
	} {
		t.Run(absence.name, func(t *testing.T) {
			var log []string
			builder := &recordingBuilder{}
			hooks := mapHooks(&log)
			hooks.IgnoringTouch = func() bool {
				log = append(log, "ignoring")

				return true
			}
			rel := &relationQuery{
				Builder: builder,
				info:    RelationInfo{Kind: KindBelongsTo, References: []RefKey{{PrimaryColumn: "id", ForeignColumn: "author_id"}}},
				parent:  mapModel{"author_id": absence.value},
				hooks:   hooks,
			}

			result, err := rel.Touch()
			require.NoError(t, err)
			assert.EqualValues(t, 0, result.RowsAffected)
			assert.Equal(t, []string{"read:author_id"}, log, "the absent-key guard runs before the ignore check")
			assert.Empty(t, builder.calls)
		})
	}

	// MorphTo carries the same override: an absent morph id means no statement.
	var log []string
	builder := &recordingBuilder{}
	rel := &relationQuery{
		Builder: builder,
		info:    RelationInfo{Kind: KindMorphTo, MorphIDColumn: "imageable_id"},
		parent:  mapModel{"imageable_id": nil},
		hooks:   mapHooks(&log),
	}

	result, err := rel.Touch()
	require.NoError(t, err)
	assert.EqualValues(t, 0, result.RowsAffected)
	assert.Equal(t, []string{"read:imageable_id"}, log)
	assert.Empty(t, builder.calls)
}

func TestRelationQuery_Touch_PresentBelongsToKeyRunsTheChain(t *testing.T) {
	var log []string
	builder := &recordingBuilder{}
	hooks := mapHooks(&log)
	hooks.UpdatedAtColumn = func() (string, error) { return "updated_at", nil }
	rel := &relationQuery{
		Builder: builder,
		info:    RelationInfo{Kind: KindBelongsTo, References: []RefKey{{PrimaryColumn: "id", ForeignColumn: "author_id"}}},
		parent:  mapModel{"author_id": uint64(9)},
		hooks:   hooks,
	}

	_, err := rel.Touch()
	require.NoError(t, err)
	assert.Equal(t, []string{"Clone", "WithoutGlobalScopes", "Update"}, builder.calls)
}

func TestRelationQuery_Touch_ExplicitColumnKeepsTheBuilderSemantics(t *testing.T) {
	var log []string
	builder := &recordingBuilder{}
	hooks := mapHooks(&log)
	hooks.IgnoringTouch = func() bool {
		log = append(log, "ignoring")

		return true
	}
	hooks.UpdatedAtColumn = func() (string, error) {
		log = append(log, "column")

		return "updated_at", nil
	}
	rel := &relationQuery{Builder: builder, info: RelationInfo{Kind: KindHasMany}, hooks: hooks}

	_, err := rel.Touch("published_at")
	require.NoError(t, err)
	assert.Empty(t, log, "an explicitly named column bypasses the model checks")
	assert.Equal(t, []string{"Clone", "WithoutGlobalScopes", "Update"}, builder.calls)
	assert.Equal(t, map[string]any{"published_at": time.Time{}}, mapValueShape(builder.args[2]))
}

// mapValueShape keeps only the keys of the update map, so the assertion is about the column name.
func mapValueShape(value any) map[string]any {
	updated, ok := value.(map[string]any)
	if !ok {
		return nil
	}

	shape := make(map[string]any, len(updated))
	for column, item := range updated {
		if _, ok := item.(time.Time); ok {
			shape[column] = time.Time{}
			continue
		}
		shape[column] = item
	}

	return shape
}

func TestRelationQuery_Touch_ColumnResolutionErrorPropagates(t *testing.T) {
	wantErr := errors.New("no column")
	builder := &recordingBuilder{}
	rel := &relationQuery{Builder: builder, info: RelationInfo{Kind: KindHasMany}, hooks: Hooks{
		UpdatedAtColumn: func() (string, error) { return "", wantErr },
	}}

	_, err := rel.Touch()
	assert.ErrorIs(t, err, wantErr)
	assert.Empty(t, builder.calls)
}

// --- RawUpdate ------------------------------------------------------------------------------------

func TestRelationQuery_RawUpdate_ClonesAndDropsGlobalScopes(t *testing.T) {
	builder := &recordingBuilder{}
	rel := &relationQuery{Builder: builder, info: RelationInfo{Kind: KindHasMany}, hooks: Hooks{}}

	result, err := rel.RawUpdate(map[string]any{"title": "x"})
	require.NoError(t, err)
	assert.EqualValues(t, 1, result.RowsAffected)
	assert.Equal(t, []string{"Clone", "WithoutGlobalScopes", "Update"}, builder.calls)
	assert.Equal(t, map[string]any{"title": "x"}, builder.args[2])
}
