package driver

import (
	"context"
	"reflect"

	contractsdriver "github.com/gradii/framework/contracts/database/driver"
	contractsquery "github.com/gradii/framework/contracts/database/query"
	querybuilder "github.com/gradii/framework/database/query"
	"github.com/gradii/framework/errors"
)

// QueryComponents binds a query compiler and result processor to one connection.
// Both must be carried together when a builder is rebound to another connection.
type QueryComponents struct {
	Grammar   contractsquery.Grammar
	Processor contractsquery.Processor
	Dialect   contractsdriver.Dialect
	// VersionSource accepts the caller's current context, including after WithContext.
	VersionSource func(context.Context) (string, error)
	Options       contractsdriver.GrammarOptions
}

func (c QueryComponents) Validate() error {
	if nilComponent(c.Grammar) || nilComponent(c.Processor) {
		return errors.DatabaseDialectComponentsMissing
	}
	return nil
}

// WithContext builds a fresh compiler when its version lookup is connection-bound.
func (c QueryComponents) WithContext(ctx context.Context) QueryComponents {
	if c.VersionSource == nil || c.Dialect == nil {
		return c
	}
	c.Options.ServerVersion = func() (string, error) { return c.VersionSource(ctx) }
	c.Grammar = c.Dialect.NewQueryGrammar(c.Options)
	return c
}

func ResolveDialect(source contractsdriver.Driver) (contractsdriver.Dialect, error) {
	if provider, ok := source.(contractsdriver.DriverWithDialect); ok {
		dialect := provider.Dialect()
		if nilComponent(dialect) {
			return nil, errors.DatabaseDialectComponentsMissing
		}
		return dialect, nil
	}
	return nil, nil
}

// ResolveQueryComponents uses the provider before considering a legacy dialect.
// Database implementations are supplied by their external driver packages.
func ResolveQueryComponents(dialect contractsdriver.Dialect, options contractsdriver.GrammarOptions) (QueryComponents, error) {
	if !nilComponent(dialect) {
		components := QueryComponents{
			Grammar: dialect.NewQueryGrammar(options), Processor: dialect.NewProcessor(), Dialect: dialect, Options: options,
		}
		if err := components.Validate(); err != nil {
			return QueryComponents{}, err
		}
		return components, nil
	}
	if querybuilder.CanonicalDialect(options.Driver) != "" {
		return QueryComponents{}, errors.DatabaseDialectRequired.Args(options.Driver)
	}
	grammar, _ := querybuilder.NewGrammar(options.Driver, options.Prefix)
	return QueryComponents{Grammar: grammar, Processor: querybuilder.NewProcessor(options.Driver), Options: options}, nil
}

func EscapeWithDialect(dialect contractsdriver.Dialect, driver string, value any, binary bool) (string, error) {
	if escaper, ok := dialect.(contractsdriver.DialectWithEscaper); ok {
		return escaper.Escape(value, binary)
	}
	return Escape(driver, value, binary)
}

func nilComponent(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan:
		return reflected.IsNil()
	default:
		return false
	}
}
