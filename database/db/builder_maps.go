package db

import (
	"context"
	"database/sql"

	"github.com/gradii/framework/errors"
	"github.com/jmoiron/sqlx"
)

type mapQueryer interface {
	QueryxContext(context.Context, string, ...any) (*sqlx.Rows, error)
}

// sqlx's struct scanner does not accept map destinations. Metadata and dynamic queries
// use MapScan while retaining the same connection, transaction, and driver values.
func selectMaps(ctx context.Context, queryer mapQueryer, dest *[]map[string]any, statement string, args ...any) (err error) {
	if dest == nil {
		return errors.DatabaseUnsupportedType.Args("*[]map[string]any", "non-nil pointer")
	}
	rows, err := queryer.QueryxContext(ctx, statement, args...)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	result := []map[string]any{}
	for rows.Next() {
		row := map[string]any{}
		if err := rows.MapScan(row); err != nil {
			return err
		}
		copyMapBytes(row)
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	*dest = result
	return nil
}
func getMap(ctx context.Context, queryer mapQueryer, dest *map[string]any, statement string, args ...any) (err error) {
	if dest == nil {
		return errors.DatabaseUnsupportedType.Args("*map[string]any", "non-nil pointer")
	}
	rows, err := queryer.QueryxContext(ctx, statement, args...)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return err
		}
		return sql.ErrNoRows
	}
	row := map[string]any{}
	if err := rows.MapScan(row); err != nil {
		return err
	}
	copyMapBytes(row)
	*dest = row
	return rows.Err()
}
func copyMapBytes(row map[string]any) {
	for key, value := range row {
		if data, ok := value.([]byte); ok && data != nil {
			row[key] = append([]byte{}, data...)
		}
	}
}
func (b *Builder) SelectContext(ctx context.Context, dest any, statement string, args ...any) error {
	if maps, ok := dest.(*[]map[string]any); ok {
		return selectMaps(ctx, b.DB, maps, statement, args...)
	}
	return b.DB.SelectContext(ctx, dest, statement, args...)
}
func (b *Builder) GetContext(ctx context.Context, dest any, statement string, args ...any) error {
	if values, ok := dest.(*map[string]any); ok {
		return getMap(ctx, b.DB, values, statement, args...)
	}
	return b.DB.GetContext(ctx, dest, statement, args...)
}
func (b *TxBuilder) SelectContext(ctx context.Context, dest any, statement string, args ...any) error {
	if maps, ok := dest.(*[]map[string]any); ok {
		return selectMaps(ctx, b.Tx, maps, statement, args...)
	}
	return b.Tx.SelectContext(ctx, dest, statement, args...)
}
func (b *TxBuilder) GetContext(ctx context.Context, dest any, statement string, args ...any) error {
	if values, ok := dest.(*map[string]any); ok {
		return getMap(ctx, b.Tx, values, statement, args...)
	}
	return b.Tx.GetContext(ctx, dest, statement, args...)
}
