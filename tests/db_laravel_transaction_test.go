package tests

import (
	"github.com/gradii/framework/contracts/database/db"
	"github.com/gradii/framework/errors"
)

func (s *DBTestSuite) TestLaravelNestedTransactionsAndCallbacks() {
	for driver, query := range s.queries {
		s.Run(driver, func() {
			connection := query.DB()
			s.Require().NoError(connection.Statement("drop table if exists port_transaction_records"))
			s.Require().NoError(connection.Statement("create table port_transaction_records (id bigint primary key)"))
			defer func() { s.NoError(connection.Statement("drop table if exists port_transaction_records")) }()
			var callbacks []string
			err := connection.Transaction(func(tx db.Tx) error {
				if _, err := tx.Table("port_transaction_records").Insert(map[string]any{"id": 1}); err != nil {
					return err
				}
				if err := tx.AfterCommit(func() error { callbacks = append(callbacks, "root"); return nil }); err != nil {
					return err
				}
				child, err := tx.BeginTransaction()
				if err != nil {
					return err
				}
				if _, err := child.Table("port_transaction_records").Insert(map[string]any{"id": 2}); err != nil {
					return err
				}
				if err := child.AfterRollback(func() error { callbacks = append(callbacks, "rollback"); return nil }); err != nil {
					return err
				}
				if err := child.Rollback(); err != nil {
					return err
				}
				child, err = tx.BeginTransaction()
				if err != nil {
					return err
				}
				if _, err := child.Table("port_transaction_records").Insert(map[string]any{"id": 3}); err != nil {
					return err
				}
				if err := child.AfterCommit(func() error { callbacks = append(callbacks, "child"); return nil }); err != nil {
					return err
				}
				return child.Commit()
			})
			s.Require().NoError(err)
			s.Equal([]string{"rollback", "child", "root"}, callbacks)
			var ids []int64
			s.Require().NoError(connection.Table("port_transaction_records").OrderBy("id").Pluck("id", &ids))
			s.Equal([]int64{1, 3}, ids)

			attempt := 0
			err = connection.Transaction(func(tx db.Tx) error {
				attempt++
				if _, err := tx.Table("port_transaction_records").Insert(map[string]any{"id": 10 + attempt}); err != nil {
					return err
				}
				if attempt == 1 {
					return errors.DatabaseDeadlock.Args("test retry")
				}
				return nil
			}, 2)
			s.Require().NoError(err)
			s.Equal(2, attempt)
			s.Require().NoError(connection.Table("port_transaction_records").OrderBy("id").Pluck("id", &ids))
			s.Equal([]int64{1, 3, 12}, ids)

			attempt = 0
			err = connection.Transaction(func(tx db.Tx) error {
				attempt++
				if _, err := tx.Table("port_transaction_records").Insert(map[string]any{"id": 20}); err != nil {
					return err
				}
				return tx.AfterCommit(func() error { return errors.DatabaseDeadlock.Args("after commit") })
			}, 3)
			s.ErrorIs(err, errors.DatabaseTransactionCallbackFailed)
			s.Equal(1, attempt)
			count, err := connection.Table("port_transaction_records").Where("id", 20).Count()
			s.Require().NoError(err)
			s.Equal(int64(1), count)
		})
	}
}
