package relations

import (
	"strings"

	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/errors"
)

// This file is the eager-load list grammar — the parser behind OrmBuilder.With / Load and the
// entry type conditions.eagerLoad stores — moved out of database/gorm by the S4 reduction slice
// (database/tmp/LARAVEL_1TO1_ALIGNMENT_REVIEW.md §C.4). The code is the gorm original verbatim:
// it depends on nothing but the contracts and the error list, so no seam was needed; only the
// entry type and the four functions database/gorm's engines call (ParseEagerLoad,
// MergeEagerLoadEntry, CombineEagerLoadEntry, DirectNestedEntries) are exported, the rest of
// the grammar stays package-private here.

// EagerLoadEntry is the normalised representation of one entry on the eager-load list. It is the
// Go equivalent of fedaco's Record<string, FedacoBuilderCallBack>, but uses a slice in the caller
// to preserve insertion order (Go maps don't).
type EagerLoadEntry struct {
	Relation string                        // e.g. "Books" or "Books.Author"
	Columns  []string                      // pruned column list parsed from "Books:id,name"; nil = SELECT *
	Callback contractsorm.RelationCallback // nil for the synthetic noop entries that _addNestedWiths inserts
}

// ParseEagerLoad normalises the variadic args accepted by OrmBuilder.With into an ordered
// slice of EagerLoadEntry. Mirrors the union of fedaco's _parseWithRelations,
// _addNestedWiths and _createSelectWithConstraint, expressed as Go runtime type-dispatch since Go
// doesn't have TypeScript-style overloads.
//
// Accepted shapes (any of which may also appear inside a single []any):
//   - "Books"
//   - "Books:id,name"                                   (column-pruned)
//   - "Books.Author"                                    (nested; auto-fills "Books" as a noop entry)
//   - "Books", callback                                 (string + callback as the only two args)
//   - "Books", "Roles", "Address"                       (multiple strings)
//   - map[string]contractsorm.RelationCallback{...}     (relation -> callback)
//   - []string{"Books", "Roles"}
//   - []any{"Books", map[string]contractsorm.RelationCallback{"Roles": cb}}
func ParseEagerLoad(args []any) ([]EagerLoadEntry, error) {
	// Special-case the (string, callback) two-arg form so q.With("Books", cb) binds the
	// callback to the string rather than treating cb as a freestanding entry.
	if len(args) == 2 {
		if name, ok := args[0].(string); ok {
			if cb, ok := toRelationCallback(args[1]); ok {
				return appendEagerLoadEntry(nil, name, cb)
			}
		}
	}

	var out []EagerLoadEntry
	for _, arg := range args {
		var err error
		out, err = mergeEagerLoadArg(out, arg)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// mergeEagerLoadArg dispatches a single arg into out, handling all accepted shapes recursively.
func mergeEagerLoadArg(out []EagerLoadEntry, arg any) ([]EagerLoadEntry, error) {
	switch v := arg.(type) {
	case nil:
		return out, nil
	case string:
		return appendEagerLoadEntry(out, v, nil)
	case []string:
		var err error
		for _, s := range v {
			out, err = appendEagerLoadEntry(out, s, nil)
			if err != nil {
				return nil, err
			}
		}
		return out, nil
	case []any:
		var err error
		for _, item := range v {
			out, err = mergeEagerLoadArg(out, item)
			if err != nil {
				return nil, err
			}
		}
		return out, nil
	case map[string]contractsorm.RelationCallback:
		return appendEagerLoadMap(out, v)
	case map[string]func(contractsorm.Query) contractsorm.Query:
		converted := make(map[string]contractsorm.RelationCallback, len(v))
		for k, fn := range v {
			converted[k] = contractsorm.RelationCallback(fn)
		}
		return appendEagerLoadMap(out, converted)
	default:
		return nil, errors.OrmEagerLoadInvalidArgument.Args(v)
	}
}

func appendEagerLoadMap(out []EagerLoadEntry, m map[string]contractsorm.RelationCallback) ([]EagerLoadEntry, error) {
	var err error
	for name, cb := range m {
		out, err = appendEagerLoadEntry(out, name, cb)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// appendEagerLoadEntry adds one (relation, callback) pair to out, applying _addNestedWiths and
// _createSelectWithConstraint semantics:
//   - "A.B.C" inserts noop entries for each missing prefix (A, A.B), then a real entry for A.B.C
//   - "Books:id,name" splits into name="Books" + columns=[id, name]
//   - duplicate relations inside one parse fold together the way Eloquent's combineConstraints folds
//     them: both constraints survive — the later-listed entry runs first — the earlier-listed entry
//     keeps its select list and its position. Replacement happens only when a later with() call
//     merges into the accumulated list, see MergeEagerLoadEntry
func appendEagerLoadEntry(out []EagerLoadEntry, raw string, cb contractsorm.RelationCallback) ([]EagerLoadEntry, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.OrmEagerLoadEmptyRelation
	}

	name, columns := splitRelationSelect(raw)
	if name == "" {
		return nil, errors.OrmEagerLoadEmptyRelation
	}

	// Walk dot-segments and ensure every prefix has an entry. Only the leaf carries the real
	// callback / columns; intermediate prefixes get a synthetic placeholder.
	segments := strings.Split(name, ".")
	progress := ""
	for i, seg := range segments {
		if seg == "" {
			return nil, errors.OrmEagerLoadEmptyRelation
		}
		if i == 0 {
			progress = seg
		} else {
			progress = progress + "." + seg
		}
		isLeaf := i == len(segments)-1
		entry := EagerLoadEntry{Relation: progress}
		if isLeaf {
			entry.Columns = columns
			entry.Callback = cb
			out = CombineEagerLoadEntry(out, entry)
		} else {
			out = upsertEagerLoadEntry(out, entry, false)
		}
	}
	return out, nil
}

// upsertEagerLoadEntry inserts entry into out, or — when an entry with the same relation already
// exists — overwrites it in place. The replace flag is false only for the synthetic prefix
// placeholders of a dotted walk inside one parse: Eloquent's addNestedWiths fills missing prefixes
// only ("we will not set it again"), so walking "Books.Author" must not erase a callback an
// earlier entry put on the same parse's "Books" prefix. Every entry a later with() merges into
// the list a builder already accumulated replaces unconditionally — see MergeEagerLoadEntry.
func upsertEagerLoadEntry(out []EagerLoadEntry, entry EagerLoadEntry, replace bool) []EagerLoadEntry {
	for i, existing := range out {
		if existing.Relation == entry.Relation {
			if replace {
				out[i] = entry
			}
			return out
		}
	}
	return append(out, entry)
}

// CombineEagerLoadEntry folds a leaf onto an entry already present in the same parse the way
// Eloquent's combineConstraints folds duplicate relation keys inside one with() call (Builder.php,
// illuminate/database v12.55.1): both constraints survive — the later-listed entry runs first, the
// earlier-listed entry second — and the earlier-listed entry keeps its select list and its position.
// Replacement happens only when a later with() call merges into the accumulated list, see
// MergeEagerLoadEntry. Oracle-pinned (v12.55.1, behavioral): with('posts:id,user_id,name', 'posts')
// still selects the pruned list; with(['posts' => cb, 'posts']) still loads only the constrained
// row; with(['posts' => cb, 'posts:id,user_id,name']) runs both; with('posts:id,user_id,name',
// 'posts:id,user_id') keeps the first list.
func CombineEagerLoadEntry(out []EagerLoadEntry, entry EagerLoadEntry) []EagerLoadEntry {
	for i, existing := range out {
		if existing.Relation != entry.Relation {
			continue
		}

		combined := existing
		if combined.Columns == nil {
			combined.Columns = entry.Columns
		}
		combined.Callback = combineEagerLoadCallbacks(existing.Callback, entry.Callback)
		out[i] = combined

		return out
	}

	return append(out, entry)
}

// combineEagerLoadCallbacks composes two constraints from one parse the way Eloquent's
// combineConstraints does: the later-listed constraint runs first and the earlier-listed one second,
// and a constraint returning nil leaves the builder as is (Laravel's $constraint($builder) ?? $builder).
func combineEagerLoadCallbacks(earlier, later contractsorm.RelationCallback) contractsorm.RelationCallback {
	if earlier == nil {
		return later
	}
	if later == nil {
		return earlier
	}

	return func(query contractsorm.Query) contractsorm.Query {
		if scoped := later(query); scoped != nil {
			query = scoped
		}
		if scoped := earlier(query); scoped != nil {
			return scoped
		}

		return query
	}
}

// MergeEagerLoadEntry folds one entry from a freshly parsed with-list into the list a builder has
// accumulated. Eloquent's Builder::with finishes with the array_merge of eagerLoads
// (Builder.php, illuminate/database v12.55.1): the later entry wins by relation KEY
// unconditionally — a plain entry replaces a constrained one exactly as a constrained entry
// replaces a plain one — while the relation keeps the position it first got. Oracle-pinned:
// with('posts', cb)->with('posts') loads every related row, with('posts')->with('posts', cb) keeps
// only the constrained read, and with('posts', cb)->with('posts.comments') rewrites the parent key
// as well — the dotted walk's fill-missing rule applies within one parse, not across merges.
func MergeEagerLoadEntry(out []EagerLoadEntry, entry EagerLoadEntry) []EagerLoadEntry {
	return upsertEagerLoadEntry(out, entry, true)
}

// splitRelationSelect splits "Books:id,name" into ("Books", ["id", "name"]). A bare relation name
// without a colon yields (name, nil). Whitespace inside the column list is trimmed.
func splitRelationSelect(raw string) (string, []string) {
	prefix, rest, ok := strings.Cut(raw, ":")
	if !ok {
		return raw, nil
	}
	name := strings.TrimSpace(prefix)
	if rest == "" {
		return name, nil
	}
	parts := strings.Split(rest, ",")
	cols := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			cols = append(cols, p)
		}
	}
	if len(cols) == 0 {
		return name, nil
	}
	return name, cols
}

// toRelationCallback accepts the two callable shapes a user is likely to pass and converts to a
// canonical contractsorm.RelationCallback. Returns ok=false for anything else.
func toRelationCallback(v any) (contractsorm.RelationCallback, bool) {
	switch fn := v.(type) {
	case nil:
		return nil, true
	case contractsorm.RelationCallback:
		return fn, true
	case func(contractsorm.Query) contractsorm.Query:
		return contractsorm.RelationCallback(fn), true
	}
	return nil, false
}

// DirectNestedEntries returns the entries from list whose relation is strictly nested under
// parent (i.e. starts with "parent."), with the "parent." prefix stripped. Used when recursing
// into a child query: "Books.Author" under parent "Books" becomes "Author".
func DirectNestedEntries(list []EagerLoadEntry, parent string) []EagerLoadEntry {
	prefix := parent + "."
	var out []EagerLoadEntry
	for _, e := range list {
		if strings.HasPrefix(e.Relation, prefix) {
			child := e
			child.Relation = strings.TrimPrefix(e.Relation, prefix)
			out = append(out, child)
		}
	}
	return out
}
