package gorm

import (
	"reflect"
	"strings"

	"github.com/gradii/framework/errors"
)

// CreateOrFirst creates the model from the given attributes (and values) without reading first,
// mirroring Eloquent's Builder::createOrFirst: the insert runs inside a savepoint when the query
// is bound to a transaction (withSavepointIfNeeded), and a unique-constraint violation is caught
// to read the existing row back over the write connection — where(attributes)->first() — before
// the error is rethrown as a typed violation.
func (r *OrmBuilder) CreateOrFirst(dest any, conds ...any) error {
	defer r.withZeroFor(conds...)()
	if err := r.applyPendingAttributes(dest); err != nil {
		return err
	}

	var find any
	if len(conds) > 0 {
		find = conds[0]
		if err := r.assignValue(dest, conds[0]); err != nil {
			return err
		}
		for _, value := range conds[1:] {
			if err := r.assignValue(dest, value); err != nil {
				return err
			}
		}
	}

	_, err := r.createWithUniqueRetry(dest, find)

	return err
}

// createWithUniqueRetry inserts dest and reports whether a unique-constraint violation sent it
// back to read an existing row the find condition names — Laravel's createOrFirst catch. The
// insert runs inside withSavepointIfNeeded so a failure inside a transaction rolls back to the
// savepoint instead of poisoning the transaction; a re-read that still finds nothing rethrows the
// violation as errors.OrmUniqueConstraintViolation.
func (r *OrmBuilder) createWithUniqueRetry(dest any, find any) (bool, error) {
	err := r.withSavepointIfNeeded(func() error { return r.Create(dest) })
	if err == nil {
		return false, nil
	}
	if !isUniqueConstraintViolation(err) {
		return false, err
	}

	query := r
	if find != nil {
		folded, foldErr := r.foldFindCondition(find)
		if foldErr != nil {
			return false, foldErr
		}
		query = folded
	}
	write, ok := query.UseWritePdo().(*OrmBuilder)
	if !ok {
		return false, err
	}
	found, readErr := write.firstRow(dest)
	if readErr != nil {
		return false, readErr
	}
	if found {
		return true, nil
	}

	return false, errors.OrmUniqueConstraintViolation.Args(err)
}

// withSavepointIfNeeded runs scope directly when the query is not inside a transaction and inside
// a savepoint when it is, mirroring Eloquent Builder::withSavepointIfNeeded: a statement that
// violates a constraint inside a transaction would otherwise poison the whole transaction, so the
// failure is rolled back to the savepoint and the caller can read what another writer committed.
func (r *OrmBuilder) withSavepointIfNeeded(scope func() error) error {
	if r.tx == nil {
		return scope()
	}

	if save := savepointStatement(r.dbConfig.Driver, "save"); save != "" {
		if _, err := r.execStatement(save, nil); err != nil {
			return err
		}
	}
	if err := scope(); err != nil {
		if rollback := savepointStatement(r.dbConfig.Driver, "rollback"); rollback != "" {
			_, _ = r.execStatement(rollback, nil)
		}
		return err
	}
	if release := savepointStatement(r.dbConfig.Driver, "release"); release != "" {
		if _, err := r.execStatement(release, nil); err != nil {
			return err
		}
	}

	return nil
}

// savepointStatement renders the savepoint statement for the action ("save", "rollback",
// "release") in the connection's dialect. The savepoint is named "trans": Laravel names its
// savepoints "trans" plus the transaction level (trans2, trans3, ...) and never releases them,
// so the fixed name and the explicit release are this port's choices, equivalent within
// withSavepointIfNeeded's scope. SQL Server spells the statements SAVE/ROLLBACK TRANSACTION and
// takes no release.
func savepointStatement(driver, action string) string {
	const name = "trans"

	if driver == "sqlserver" {
		switch action {
		case "save":
			return "SAVE TRANSACTION " + name
		case "rollback":
			return "ROLLBACK TRANSACTION " + name
		}
		return ""
	}

	switch action {
	case "save":
		return "SAVEPOINT " + name
	case "rollback":
		return "ROLLBACK TO SAVEPOINT " + name
	case "release":
		return "RELEASE SAVEPOINT " + name
	}
	return ""
}

// isUniqueConstraintViolation reports whether err is a unique-constraint violation the way each
// supported connection classifies one (Connection::isUniqueConstraintError): MySQL 1062, Postgres
// SQLSTATE 23505, SQLite's "UNIQUE constraint failed" / "not unique" messages, SQL Server's
// duplicate-key message. The driver error arrives unwrapped, so the classification is by SQLSTATE
// when the error exposes one and by message otherwise.
func isUniqueConstraintViolation(err error) bool {
	if err == nil {
		return false
	}

	if state, ok := err.(interface{ SQLState() string }); ok && state.SQLState() == "23505" {
		return true
	}
	if code, ok := err.(interface{ Code() string }); ok && code.Code() == "23505" {
		return true
	}
	if number, ok := err.(interface{ Number() uint16 }); ok && number.Number() == 1062 {
		return true
	}

	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "integrity constraint violation: 1062"),
		strings.Contains(message, "error 1062"),
		strings.Contains(message, "duplicate entry"):
		return true
	case strings.Contains(message, "23505"),
		strings.Contains(message, "duplicate key value violates unique constraint"):
		return true
	case strings.Contains(message, "unique constraint failed"),
		strings.Contains(message, "not unique"):
		return true
	case strings.Contains(message, "cannot insert duplicate key row in object"),
		strings.Contains(message, "violation of unique key constraint"),
		strings.Contains(message, "duplicate key"):
		return true
	}

	return false
}

// firstOrCreateWithCreated is FirstOrCreate carrying the created-vs-found signal Eloquent keeps on
// the instance — $wasRecentlyCreated — which a Go model has no flag for: it reports true when the
// row was just inserted, and false when an existing row was read, by the lookup itself or by
// createOrFirst's re-read after a unique constraint another writer tripped. IncrementOrCreate is
// the signal's consumer; the public FirstOrCreate has nothing to expose it on.
func (r *OrmBuilder) firstOrCreateWithCreated(dest any, conds ...any) (bool, error) {
	if len(conds) == 0 {
		return false, errors.OrmQueryConditionRequired
	}

	found, err := r.firstOrInit(dest, conds[0], conds[1:], nil)
	if err != nil {
		return false, err
	}
	if found {
		// firstOrInit has already run the retrieved hooks.
		return false, nil
	}

	// The row must be created: firstOrCreate routes a miss through createOrFirst, so a unique
	// constraint another writer tripped reads the winning row back instead of failing.
	readBack, err := r.createWithUniqueRetry(dest, conds[0])
	if err != nil {
		return false, err
	}

	return !readBack, nil
}

// IncrementOrCreate finds the first record that matches the given attributes, or creates one when
// none matches, and — only when the record already existed — steps it, mirroring Eloquent's
// Builder::incrementOrCreate: firstOrCreate($attributes, [$column => $default]) followed, unless
// wasRecentlyCreated, by Model::increment. The step is incrementModel's; args carries Laravel's
// optional parameters positionally, since Go signatures cannot declare defaults: a string names
// the column (default "count"), the first number is the create default and the second the step
// (default 1 each), and a map is the extra values.
func (r *OrmBuilder) IncrementOrCreate(dest any, attributes any, args ...any) error {
	column, defaultAmount, step, extra := incrementOrCreateArgs(args)
	// The create default is an explicit value like the attributes: Eloquent inserts it even when
	// it is 0 (oracle v12.55.1, /tmp/r15v/probe_v.out P1: `insert into "counters" ("name",
	// "count", ...)` with count = 0), so its column joins WithZero beside the attribute keys.
	defer r.withZeroFor(attributes, extra, map[string]any{column: defaultAmount})()

	created, err := r.firstOrCreateWithCreated(dest, attributes, map[string]any{column: defaultAmount})
	if err != nil {
		return err
	}
	if created {
		return nil
	}

	return r.incrementModel(dest, column, step, extra)
}

// incrementModel runs one found record through Model::increment — incrementOrDecrement's Go form.
// The column moves on the model and the extra values fill first, the way Eloquent writes
// `$this->{$column} = $this->{$column} + $amount; $this->forceFill($extra);` before the updating
// event, so a handler observes the stepped model (oracle v12.55.1, executed:
// /tmp/cof_inc_events_probe.php — the updating listener reads count=2). The updating event then
// runs; a handler's error aborts the write (the Go reading of Eloquent's `return false`). The
// statement compiles on a fresh, scope-less query scoped to the model's key — Laravel's
// setKeysForSaveQuery(newQueryWithoutScopes()) — as the raw `<column> + <step>` expression, the
// extras and the automatic updated-at stamp; and the updated event runs. Eloquent then syncs the
// incremented column into the model's original bag; the port keeps no such bag (the implicit
// original-value tracking was removed by the user's decision of 2026-11-06), so a later Save of the
// model writes its non-zero fields and a stamped updated_at like any other keyed Save.
func (r *OrmBuilder) incrementModel(dest any, column string, step any, extra map[string]any) error {
	if _, ok := numericString(step); !ok {
		return errors.OrmQueryIncrementNonNumeric.Args(step, "increment")
	}

	stepped, err := r.incrementModelAmount(dest, column, step)
	if err != nil {
		return err
	}
	if err := r.assignColumn(dest, column, stepped); err != nil {
		return err
	}
	for name, value := range extra {
		if err := r.assignColumn(dest, name, value); err != nil {
			return err
		}
	}

	keyed := r.modelIncrementQuery(dest)

	if _, err := keyed.updating(dest); err != nil {
		return err
	}

	assignments, err := keyed.updateAssignments(keyed.stepValues(column, "+", []any{step, extra}))
	if err != nil {
		return err
	}
	statement, bindings, err := keyed.compileUpdateScoped(assignments, false)
	if err != nil {
		return err
	}
	if _, err := keyed.execStatement(statement, bindings); err != nil {
		return err
	}
	if _, err := keyed.updated(dest); err != nil {
		return err
	}

	return nil
}

// modelIncrementQuery builds the query the increment statement runs on: Laravel's
// newQueryWithoutScopes() — a fresh query on the model, carrying none of the receiver's wheres or
// scopes — scoped to the model's key by the update path's own key where (setKeysForSaveQuery's Go
// form: applyUpdateKeyWhere adds the key because the query carries no destination of its own),
// with the soft-delete predicate lifted the way a fresh read lifts it.
func (r *OrmBuilder) modelIncrementQuery(dest any) *OrmBuilder {
	query := r.fresh()
	if built, ok := query.Model(dest).(*OrmBuilder); ok {
		query = built
	}
	query = query.WithoutGlobalScopes("*").(*OrmBuilder)
	query = query.WithTrashed().(*OrmBuilder)

	return query
}

// incrementModelAmount reads the model's current column value and adds the step the way PHP's
// numeric + does: an integer pair adds as an integer, a float on either side makes the sum a
// float, and nil — Eloquent's null attribute — counts as zero. A column the model does not declare
// reads as the step alone: there is no field to hold the new value, and only the statement carries
// it.
func (r *OrmBuilder) incrementModelAmount(dest any, column string, step any) (any, error) {
	schema, err := parseSchema(dest)
	if err != nil {
		return nil, err
	}

	field, ok := schema.FieldsByName[column]
	if !ok {
		field, ok = schema.FieldsByDBName[column]
	}
	if !ok {
		return step, nil
	}

	current, _ := field.ValueOf(r.ctx, reflect.ValueOf(dest))

	return addModelAmount(current, step), nil
}

// incrementOrCreateArgs unpacks IncrementOrCreate's optional tail: a string names the column, a
// map is the extra values, and numbers fill the create default and the step in that order —
// Laravel's ($column, $default, $step, $extra) read positionally, with Laravel's defaults.
func incrementOrCreateArgs(args []any) (string, any, any, map[string]any) {
	column := "count"
	defaultAmount := any(1)
	step := any(1)
	var extra map[string]any
	numerics := 0

	for _, arg := range args {
		if name, ok := arg.(string); ok {
			column = name

			continue
		}
		if mapping, ok := asColumnMap(arg); ok {
			extra = mapping

			continue
		}

		numerics++
		if numerics == 1 {
			defaultAmount = arg
		} else {
			step = arg
		}
	}

	return column, defaultAmount, step, extra
}

// addModelAmount applies PHP's numeric + to the shapes a model column carries: two integer-kind
// values add as an int64, a float on either side makes the sum a float64, and nil counts as zero
// and yields the amount itself. A value that is not numeric falls back to the amount alone — the
// nearest Go reading of PHP's coercion for a column a statement then governs.
func addModelAmount(current, amount any) any {
	if current == nil {
		return amount
	}

	currentValue, amountValue := reflect.ValueOf(current), reflect.ValueOf(amount)

	if isFloatKind(currentValue.Kind()) || isFloatKind(amountValue.Kind()) {
		currentFloat, currentOK := floatValue(currentValue)
		amountFloat, amountOK := floatValue(amountValue)
		if !currentOK || !amountOK {
			return amount
		}

		return currentFloat + amountFloat
	}

	currentInt, currentOK := intValue(currentValue)
	amountInt, amountOK := intValue(amountValue)
	if !currentOK || !amountOK {
		return amount
	}

	return currentInt + amountInt
}

// isFloatKind reports whether a reflect kind is one of Go's two float kinds.
func isFloatKind(kind reflect.Kind) bool {
	return kind == reflect.Float32 || kind == reflect.Float64
}

// floatValue reads an integer- or float-kind value as a float64.
func floatValue(value reflect.Value) (float64, bool) {
	switch {
	case value.CanInt():
		return float64(value.Int()), true
	case value.CanUint():
		return float64(value.Uint()), true
	case isFloatKind(value.Kind()):
		return value.Float(), true
	}

	return 0, false
}

// intValue reads an integer-kind value as an int64.
func intValue(value reflect.Value) (int64, bool) {
	switch {
	case value.CanInt():
		return value.Int(), true
	case value.CanUint():
		return int64(value.Uint()), true
	}

	return 0, false
}
