package db

import "time"

// QueryExecuted describes a successfully executed or pretended database operation.
type QueryExecuted struct {
	SQL            string
	Bindings       []any
	Duration       time.Duration
	ConnectionName string
}
