package tests

import (
	"database/sql"
	"os"
	"reflect"
	"testing"
	"time"

	contractsschema "github.com/gradii/framework/contracts/database/schema"
	"github.com/gradii/framework/support/carbon"
	"github.com/gradii/sqlite"
	"github.com/gradii/sqlserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type timestampReviewModel[T any] struct {
	ID        int    `gorm:"column:id;primaryKey"`
	Label     string `gorm:"column:label"`
	CreatedAt T      `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt T      `gorm:"column:updated_at;autoUpdateTime"`
}

func (timestampReviewModel[T]) TableName() string { return "review_timestamps" }

type timestampReviewCase struct {
	name  string
	model func(int, *time.Time) any
	value func(time.Time) any
}

func timestampReviewShape[T any](name string, convert func(time.Time) T) timestampReviewCase {
	return timestampReviewCase{name: name, value: func(value time.Time) any { return convert(value) }, model: func(id int, value *time.Time) any {
		model := &timestampReviewModel[T]{ID: id, Label: "timestamp"}
		if value != nil {
			model.CreatedAt, model.UpdatedAt = convert(*value), convert(*value)
		}
		return model
	}}
}

func timestampReviewCases() []timestampReviewCase {
	return []timestampReviewCase{
		timestampReviewShape("carbon.DateTime", func(t time.Time) carbon.DateTime { return *carbon.NewDateTime(carbon.FromStdTime(t)) }),
		timestampReviewShape("pointer_carbon.DateTime", func(t time.Time) *carbon.DateTime { return carbon.NewDateTime(carbon.FromStdTime(t)) }),
		timestampReviewShape("time.Time", func(t time.Time) time.Time { return t }),
		timestampReviewShape("pointer_time.Time", func(t time.Time) *time.Time { return &t }),
	}
}

func timestampReviewTime(t *testing.T, value any) time.Time {
	t.Helper()
	if value == nil || reflect.ValueOf(value).IsZero() {
		return time.Time{}
	}
	switch value := value.(type) {
	case time.Time:
		return value
	case *time.Time:
		return *value
	case carbon.DateTime:
		return value.StdTime()
	case *carbon.DateTime:
		if reflect.ValueOf(*value).IsZero() {
			return time.Time{}
		}
		return value.StdTime()
	default:
		t.Fatalf("unexpected timestamp type %T", value)
		return time.Time{}
	}
}

func timestampReviewField(model any, name string) reflect.Value {
	return reflect.ValueOf(model).Elem().FieldByName(name)
}

func assertTimestampReviewNow(t *testing.T, model any, field string, started time.Time) {
	t.Helper()
	actual := timestampReviewTime(t, timestampReviewField(model, field).Interface())
	assert.False(t, actual.IsZero(), "%s must be automatically populated", field)
	assert.True(t, !actual.Before(started.Add(-time.Second)) && !actual.After(time.Now().Add(time.Second)), "%s=%s is outside the operation window", field, actual)
}

func assertTimestampReviewEqual(t *testing.T, model any, field string, want time.Time) {
	t.Helper()
	actual := timestampReviewTime(t, timestampReviewField(model, field).Interface())
	assert.True(t, want.Equal(actual), "%s: want %s, got %s", field, want, actual)
}

func TestTimestampShapesReview(t *testing.T) {
	queries := NewTestQueryBuilder().All("", false)
	if queries[sqlserver.Name] == nil && os.Getenv("GRADII_TEST_SQLSERVER_DSN") != "" {
		queries[sqlserver.Name] = NewTestQueryBuilder().SqlserverWithTimezone("UTC")
	}
	manual := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)
	manualUpdate := manual.AddDate(1, 0, 0)
	for name, fixture := range queries {
		t.Run(name, func(t *testing.T) {
			schema := newSchema(fixture, queries)
			require.NoError(t, schema.Create("review_timestamps", func(b contractsschema.Blueprint) {
				b.Integer("id")
				b.Primary("id")
				b.String("label")
				b.TimestampTz("created_at").Nullable()
				b.TimestampTz("updated_at").Nullable()
			}))
			t.Cleanup(func() {
				assert.NoError(t, schema.DropIfExists("review_timestamps"))
				if name == sqlite.Name {
					// Release the unique pool before deleting the fixture's database file.
					assert.NoError(t, fixture.DB().Purge())
					database, err := fixture.Driver().Docker()
					if assert.NoError(t, err) {
						assert.NoError(t, database.Shutdown())
					}
				}
			})
			id := 0
			for _, tc := range timestampReviewCases() {
				t.Run(tc.name, func(t *testing.T) {
					create := func(t *testing.T, stamp *time.Time) any {
						t.Helper()
						id++
						model := tc.model(id, stamp)
						require.NoError(t, fixture.Query().Create(model))
						return model
					}
					read := func(t *testing.T, id int) any {
						t.Helper()
						loaded := tc.model(0, nil)
						require.NoError(t, fixture.Query().Where("id", id).First(loaded))
						return loaded
					}
					t.Run("CreateAutomatic", func(t *testing.T) {
						started := time.Now()
						model := create(t, nil)
						loaded := read(t, id)
						for _, field := range []string{"CreatedAt", "UpdatedAt"} {
							assertTimestampReviewNow(t, model, field, started)
							assertTimestampReviewNow(t, loaded, field, started)
						}
						created, updated := timestampReviewField(model, "CreatedAt"), timestampReviewField(model, "UpdatedAt")
						if created.Kind() == reflect.Pointer && !created.IsNil() && !updated.IsNil() {
							assert.NotEqual(t, created.Pointer(), updated.Pointer(), "automatic timestamps must own independent pointer storage")
						}
						// Mutating the update timestamp must not mutate a created timestamp
						// through a shared pointer, even when the database row stays correct.
						createdBefore := timestampReviewTime(t, created.Interface())
						_, err := fixture.Query().Model(model).Where("id", id).Update("updated_at", manualUpdate)
						require.NoError(t, err)
						assertTimestampReviewEqual(t, model, "CreatedAt", createdBefore)
						assertTimestampReviewEqual(t, model, "UpdatedAt", manualUpdate)
					})
					t.Run("CreateManual", func(t *testing.T) {
						create(t, &manual)
						loaded := read(t, id)
						assertTimestampReviewEqual(t, loaded, "CreatedAt", manual)
						assertTimestampReviewEqual(t, loaded, "UpdatedAt", manual)
					})
					t.Run("CreateUnaddressableValue", func(t *testing.T) {
						id++
						model := reflect.ValueOf(tc.model(id, nil)).Elem().Interface()
						started := time.Now()
						require.NoError(t, fixture.Query().Create(model))
						loaded := read(t, id)
						assertTimestampReviewNow(t, loaded, "CreatedAt", started)
						assertTimestampReviewNow(t, loaded, "UpdatedAt", started)
					})
					t.Run("SaveAutomatic", func(t *testing.T) {
						create(t, &manual)
						model := read(t, id)
						updated := timestampReviewField(model, "UpdatedAt")
						updated.Set(reflect.Zero(updated.Type()))
						timestampReviewField(model, "Label").SetString("saved")
						started := time.Now()
						require.NoError(t, fixture.Query().Save(model))
						assertTimestampReviewEqual(t, model, "CreatedAt", manual)
						assertTimestampReviewNow(t, model, "UpdatedAt", started)
						loaded := read(t, id)
						assertTimestampReviewEqual(t, loaded, "CreatedAt", manual)
						assertTimestampReviewNow(t, loaded, "UpdatedAt", started)
					})
					t.Run("SaveManual", func(t *testing.T) {
						create(t, &manual)
						model := read(t, id)
						timestampReviewField(model, "UpdatedAt").Set(reflect.ValueOf(tc.value(manualUpdate)))
						require.NoError(t, fixture.Query().Save(model))
						loaded := read(t, id)
						assertTimestampReviewEqual(t, loaded, "CreatedAt", manual)
						assertTimestampReviewEqual(t, loaded, "UpdatedAt", manualUpdate)
					})
					t.Run("UpdateAutomatic", func(t *testing.T) {
						model := create(t, &manual)
						started := time.Now()
						_, err := fixture.Query().Model(model).Where("id", id).Update("label", "updated")
						require.NoError(t, err)
						assertTimestampReviewEqual(t, model, "CreatedAt", manual)
						assertTimestampReviewNow(t, model, "UpdatedAt", started)
						loaded := read(t, id)
						assertTimestampReviewEqual(t, loaded, "CreatedAt", manual)
						assertTimestampReviewNow(t, loaded, "UpdatedAt", started)
					})
					t.Run("UpdateManual", func(t *testing.T) {
						model := create(t, &manual)
						_, err := fixture.Query().Model(model).Where("id", id).Update(map[string]any{"label": "updated", "updated_at": tc.value(manualUpdate)})
						require.NoError(t, err)
						assertTimestampReviewEqual(t, model, "CreatedAt", manual)
						assertTimestampReviewEqual(t, model, "UpdatedAt", manualUpdate)
						loaded := read(t, id)
						assertTimestampReviewEqual(t, loaded, "CreatedAt", manual)
						assertTimestampReviewEqual(t, loaded, "UpdatedAt", manualUpdate)
					})
					t.Run("ReadNullIntoPreviouslyPopulatedFields", func(t *testing.T) {
						id++
						_, err := fixture.DB().Table("review_timestamps").Insert(map[string]any{"id": id, "label": "null timestamps", "created_at": nil, "updated_at": nil})
						require.NoError(t, err)
						loaded := tc.model(0, &manual)
						require.NoError(t, fixture.Query().Where("id", id).First(loaded))
						for _, name := range []string{"CreatedAt", "UpdatedAt"} {
							field := timestampReviewField(loaded, name)
							if field.Kind() == reflect.Pointer {
								assert.True(t, field.IsNil(), "SQL NULL must restore a nil pointer")
							} else {
								expected := reflect.New(field.Type())
								expected.Elem().Set(reflect.ValueOf(tc.value(manual)))
								if scanner, ok := expected.Interface().(sql.Scanner); ok {
									// A value Scanner owns its NULL semantics. Carbon's Scan(nil)
									// keeps the prior value; the ORM must preserve that contract.
									require.NoError(t, scanner.Scan(nil))
									assertTimestampReviewEqual(t, loaded, name, timestampReviewTime(t, expected.Elem().Interface()))
								} else {
									assert.True(t, timestampReviewTime(t, field.Interface()).IsZero())
								}
							}
						}
					})
					if reflect.TypeOf(tc.value(manual)).Kind() == reflect.Pointer {
						t.Run("UpdateSharedManualPointer", func(t *testing.T) {
							id++
							shared := tc.value(manual)
							model := tc.model(id, nil)
							timestampReviewField(model, "CreatedAt").Set(reflect.ValueOf(shared))
							timestampReviewField(model, "UpdatedAt").Set(reflect.ValueOf(shared))
							require.NoError(t, fixture.Query().Create(model))
							for _, source := range []struct {
								value any
								want  time.Time
							}{{manualUpdate, manualUpdate}, {shared, manual}} {
								_, err := fixture.Query().Model(model).Where("id", id).Update("updated_at", source.value)
								require.NoError(t, err)
								assert.True(t, manual.Equal(timestampReviewTime(t, shared)), "caller-owned time must not change")
								assertTimestampReviewEqual(t, model, "CreatedAt", manual)
								assertTimestampReviewEqual(t, model, "UpdatedAt", source.want)
								assert.NotEqual(t, timestampReviewField(model, "CreatedAt").Pointer(), timestampReviewField(model, "UpdatedAt").Pointer())
								loaded := read(t, id)
								assertTimestampReviewEqual(t, loaded, "CreatedAt", manual)
								assertTimestampReviewEqual(t, loaded, "UpdatedAt", source.want)
							}
						})
					}
					for _, pointers := range []bool{false, true} {
						operation := "BulkAutomaticValues"
						if pointers {
							operation = "BulkAutomaticPointers"
						}
						t.Run(operation, func(t *testing.T) {
							typ := reflect.TypeOf(tc.model(0, nil))
							if !pointers {
								typ = typ.Elem()
							}
							batch := reflect.MakeSlice(reflect.SliceOf(typ), 2, 2)
							ids := []int{}
							for i := range 2 {
								id++
								ids = append(ids, id)
								model := reflect.ValueOf(tc.model(id, nil))
								if !pointers {
									model = model.Elem()
								}
								batch.Index(i).Set(model)
							}
							started := time.Now()
							batchPointer := reflect.New(batch.Type())
							batchPointer.Elem().Set(batch)
							require.NoError(t, fixture.Query().Create(batchPointer.Interface()))
							for i := range batch.Len() {
								model := batch.Index(i)
								if !pointers {
									model = model.Addr()
								}
								assertTimestampReviewNow(t, model.Interface(), "CreatedAt", started)
								assertTimestampReviewNow(t, model.Interface(), "UpdatedAt", started)
							}
							for _, id := range ids {
								loaded := read(t, id)
								assertTimestampReviewNow(t, loaded, "CreatedAt", started)
								assertTimestampReviewNow(t, loaded, "UpdatedAt", started)
							}
						})
						operation = "BulkManualValues"
						if pointers {
							operation = "BulkManualPointers"
						}
						t.Run(operation, func(t *testing.T) {
							typ := reflect.TypeOf(tc.model(0, nil))
							if !pointers {
								typ = typ.Elem()
							}
							batch := reflect.MakeSlice(reflect.SliceOf(typ), 2, 2)
							ids := make([]int, batch.Len())
							for i := range batch.Len() {
								id++
								ids[i] = id
								model := reflect.ValueOf(tc.model(id, &manual))
								if !pointers {
									model = model.Elem()
								}
								batch.Index(i).Set(model)
							}
							batchPointer := reflect.New(batch.Type())
							batchPointer.Elem().Set(batch)
							require.NoError(t, fixture.Query().Create(batchPointer.Interface()))
							for i, id := range ids {
								model := batch.Index(i)
								if !pointers {
									model = model.Addr()
								}
								loaded := read(t, id)
								for _, field := range []string{"CreatedAt", "UpdatedAt"} {
									assertTimestampReviewEqual(t, model.Interface(), field, manual)
									assertTimestampReviewEqual(t, loaded, field, manual)
								}
							}
						})
					}
				})
			}
		})
	}
}
