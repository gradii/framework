package factory

import (
	"maps"
	"reflect"

	"github.com/gradii/framework/contracts/database/factory"
	ormcontract "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/database/internal/reflectx"
	rowscan "github.com/gradii/framework/database/internal/scan"
	"github.com/gradii/framework/errors"
)

// Factory attributes name Go fields, including embedded fields. Database column
// tags belong to the persistence layer and do not rename factory attributes.
var attributeMapper = reflectx.NewMapperFunc("", func(name string) string { return name })

type FactoryImpl struct {
	count *int              // number of models to generate
	query ormcontract.Query // query instance
}

func NewFactoryImpl(query ormcontract.Query) *FactoryImpl {
	return &FactoryImpl{
		query: query,
	}
}

// Count Specify the number of models you wish to create / make.
func (f *FactoryImpl) Count(count int) ormcontract.Factory {
	return f.newInstance(map[string]any{"count": count})
}

// Create a model and persist it in the database.
func (f *FactoryImpl) Create(value any, attributes ...map[string]any) error {
	if err := f.Make(value, attributes...); err != nil {
		return err
	}

	return f.query.Create(value)
}

// CreateQuietly create a model and persist it in the database without firing any events.
func (f *FactoryImpl) CreateQuietly(value any, attributes ...map[string]any) error {
	if err := f.Make(value, attributes...); err != nil {
		return err
	}

	return f.query.WithoutEvents().Create(value)
}

// Make a model instance that's not persisted in the database.
func (f *FactoryImpl) Make(value any, attributes ...map[string]any) error {
	reflectValue := reflect.Indirect(reflect.ValueOf(value))
	switch reflectValue.Kind() {
	case reflect.Array, reflect.Slice:
		count := 1
		if f.count != nil {
			count = *f.count
		}
		for i := 0; i < count; i++ {
			elemValue := reflect.New(reflectValue.Type().Elem()).Interface()
			attributes, err := getRawAttributes(elemValue, attributes...)
			if err != nil {
				return err
			}
			if attributes == nil {
				return errors.OrmFactoryMissingAttributes.SetModule(errors.ModuleOrm)
			}
			if err := rowscan.Attributes(attributes, elemValue, attributeMapper); err != nil {
				return err
			}
			reflectValue = reflect.Append(reflectValue, reflect.ValueOf(elemValue).Elem())
		}

		reflect.ValueOf(value).Elem().Set(reflectValue)

		return nil
	default:
		attributes, err := getRawAttributes(value, attributes...)
		if err != nil {
			return err
		}
		if attributes == nil {
			return errors.OrmFactoryMissingAttributes.SetModule(errors.ModuleOrm)
		}
		return rowscan.Attributes(attributes, value, attributeMapper)
	}
}

// newInstance create a new factory instance.
func (f *FactoryImpl) newInstance(attributes ...map[string]any) ormcontract.Factory {
	instance := &FactoryImpl{
		count: f.count,
		query: f.query,
	}

	if len(attributes) > 0 {
		attr := attributes[0]
		if count, ok := attr["count"].(int); ok {
			instance.count = &count
		}
	}

	return instance
}

func getRawAttributes(value any, attributes ...map[string]any) (map[string]any, error) {
	factoryModel, exist := value.(factory.Model)
	if !exist {
		return nil, errors.OrmFactoryMissingMethod.Args(reflect.TypeOf(value).String()).SetModule(errors.ModuleOrm)
	}

	definition := factoryModel.Factory().Definition()
	if len(attributes) > 0 {
		maps.Copy(definition, attributes[0])
	}

	return definition, nil
}
