package gorm

import (
	"strings"

	"github.com/gradii/framework/errors"
)

// CreateOrFirst creates the model from the given attributes (and values) without reading first,
// mirroring Eloquent's Builder::createOrFirst: the insert runs inside a savepoint when the query
// is bound to a transaction (withSavepointIfNeeded), and a unique-constraint violation is caught
// to read the existing row back over the write connection — where(attributes)->first() — before
// the error is rethrown as a typed violation.
func (r *OrmBuilder) CreateOrFirst(dest any, conds ...any) error {
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
