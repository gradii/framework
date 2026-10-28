package schema

import (
	"slices"

	"github.com/gradii/framework/contracts/database/driver"
	contractsschema "github.com/gradii/framework/contracts/database/schema"
	"github.com/gradii/framework/errors"
)

// Scope discovery and the operation must observe the same writer session. An
// empty resolved scope is not the unfiltered metadata API's "all namespaces".
func (r *Schema) dropAllScoped(kind string) error {
	return r.WithWriteConnection(func(scoped contractsschema.Schema) error {
		s := scoped.(*Schema)
		names, err := s.GetCurrentSchemaListing()
		if err != nil {
			return err
		}
		if len(names) == 0 {
			return nil
		}
		if slices.Contains(names, "") {
			return errors.SchemaCurrentNamespaceUnavailable
		}
		check := func(namespace, name string) error {
			if !slices.Contains(names, namespace) {
				return errors.SchemaObjectOutsideScope.Args(namespace, name)
			}
			return nil
		}
		operation := driver.SchemaOperation{Kind: kind}
		switch kind {
		case "dropAllTables":
			operation.Tables, err = s.GetTables(names...)
			if err != nil {
				return err
			}
			for _, table := range operation.Tables {
				if err := check(table.Schema, table.Name); err != nil {
					return err
				}
			}
		case "dropAllViews":
			operation.Views, err = s.GetViews(names...)
			if err != nil {
				return err
			}
			for _, view := range operation.Views {
				if err := check(view.Schema, view.Name); err != nil {
					return err
				}
			}
		case "dropAllTypes":
			operation.Types, err = s.GetTypes(names...)
			if err != nil {
				return err
			}
			for _, typ := range operation.Types {
				if err := check(typ.Schema, typ.Name); err != nil {
					return err
				}
			}
		}
		return s.executeSchemaOperation(operation)
	})
}
