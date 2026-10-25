package db

import (
	"database/sql"
	"fmt"
	"reflect"

	rowscan "github.com/goravel/framework/database/internal/scan"
	"github.com/goravel/framework/errors"
)

// Aggregates may return NULL, integers, floats or decimal text depending on the driver.
// Reuse row conversion rules while preserving nullable destinations through sql.Scanner.
func scanAggregate(value, dest any) error {
	target := reflect.ValueOf(dest)
	if !target.IsValid() || target.Kind() != reflect.Pointer || target.IsNil() {
		return errors.DatabaseUnsupportedType.Args(fmt.Sprintf("%T", dest), "non-nil pointer")
	}
	if scanner, ok := dest.(sql.Scanner); ok {
		return scanner.Scan(value)
	}
	if value == nil {
		clearDestination(dest)
		return nil
	}
	return rowscan.Assign(target.Elem(), value)
}
