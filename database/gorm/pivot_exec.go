package gorm

import (
	contractsorm "github.com/gradii/framework/contracts/database/orm"
	"github.com/gradii/framework/database/gorm/relations"
	"github.com/gradii/framework/errors"
)

// This file is the one gorm-side implementation site of the frozen pivot execution surface
// relations.Pivot runs on — the design note, frozen shape and oracle evidence live in
// database/gorm/relations/pivot.go, and relations/wrap.go declares the methods. Four methods
// cover everything the pivot cannot express from the gorm-free subpackage: a table-scoped query
// builder for the pivot's own table and connection, the parent-model metadata reads, and the two
// AsPivot calls that run through this ORM's own machinery (touchOwners, the model-event
// dispatch).

// NewPivotQuery builds the fresh builder AsPivot's newQueryWithoutRelationships() /
// newQueryWithoutScopes() / newModelQuery() hand the pivot: the pivot's own table on the pivot's
// connection. The builder's own connection — and "" — keeps this query, so its transaction,
// writer and grammar carry over; any other name resolves through the config exactly the way
// refreshConnection resolves a model's declared connection, cached on the queries map.
func (r *OrmBuilder) NewPivotQuery(table string, connection string) (contractsorm.Query, error) {
	if connection == "" || connection == r.dbConfig.Connection {
		return r.pivotTableQuery(table), nil
	}

	query, ok := r.queries[connection]
	if !ok || !query.ConnectionIsCurrent() {
		if r.config == nil {
			return nil, errors.DatabaseConfigNotFound
		}

		var err error
		query, _, err = BuildQuery(r.ctx, r.config, connection, r.log, r.modelToObserver, r.telemetryResolver)
		if err != nil {
			return nil, err
		}

		if r.queries == nil {
			r.queries = make(map[string]*OrmBuilder)
		}
		r.queries[connection] = query
	}

	return query.pivotTableQuery(table), nil
}

// ReadPivotParent projects the parent-model metadata AsPivot reads through its pivotParent link:
// the declared connection name AsPivot.php:57 stamps at construction, and the created-at /
// updated-at column names the timestamps checks and update stamping read
// (AsPivot.php:213,225,242).
//
// The column names resolve from the parent's declared Go schema the way the touch machinery
// resolves them (touchedUpdatedAtColumn in touching.go): the automatic field's column, then the
// conventional created_at / updated_at column, else "" — the Go reading of PHP's null, which is
// what gates the pivot's timestamps flag. Laravel itself returns class constants here; the
// schema resolution is this ORM's documented timestamp convention, and a parent that declares
// no timestamp fields reports "".
func (r *OrmBuilder) ReadPivotParent(parent any) (relations.PivotParentInfo, error) {
	info := relations.PivotParentInfo{Connection: declaredConnectionName(parent)}
	if parent == nil {
		return info, nil
	}

	createdAt, err := pivotCreatedAtColumn(parent)
	if err != nil {
		return info, err
	}

	updatedAt, err := touchedUpdatedAtColumn(parent)
	if err != nil {
		return info, err
	}

	info.CreatedAtColumn = createdAt
	info.UpdatedAtColumn = updatedAt

	return info, nil
}

// pivotCreatedAtColumn mirrors touchedUpdatedAtColumn for the created-at side: the column of the
// automatic created-at field, the conventional created_at column, or "" when the model declares
// neither.
func pivotCreatedAtColumn(parent any) (string, error) {
	schema, err := parseSchema(parent)
	if err != nil {
		return "", err
	}

	for _, field := range schema.Fields {
		if field.AutoCreateTime != 0 && field.AutoUpdateTime == 0 {
			return field.DBName, nil
		}
	}

	if field, ok := schema.FieldsByDBName["created_at"]; ok {
		return field.DBName, nil
	}

	return "", nil
}

// PivotTouchOwners runs AsPivot::delete's touchOwners() call on the pivot through the ORM's own
// walk (touching.go touchOwners). A base pivot declares no touching relations, so — like
// Laravel's Model with its default $touches = [] — the walk resolves to nothing today, and a
// pivot shape that does declare relations is walked instead of skipped.
func (r *OrmBuilder) PivotTouchOwners(pivot any) error {
	return r.touchOwners(pivot)
}

// FirePivotEvent dispatches one model event on the pivot through the ORM's event machinery: the
// observer or DispatchesEvents handler registered for the pivot's type receives the event, and
// with none registered the call is the no-op Laravel's fireModelEvent is without a dispatcher.
// The PHP listener's `return false` cancel has no Go form: a handler reports failure by
// returning an error, which the caller propagates and the operation aborts on.
func (r *OrmBuilder) FirePivotEvent(name string, pivot any) error {
	eventType, ok := pivotEventType(name)
	if !ok {
		return nil
	}

	_, err := r.event(eventType, nil, pivot)

	return err
}

// pivotEventType maps the pivot event names AsPivot and Model::save fire onto the contract's
// EventType values.
func pivotEventType(name string) (contractsorm.EventType, bool) {
	switch name {
	case "saving":
		return contractsorm.EventSaving, true
	case "saved":
		return contractsorm.EventSaved, true
	case "creating":
		return contractsorm.EventCreating, true
	case "created":
		return contractsorm.EventCreated, true
	case "updating":
		return contractsorm.EventUpdating, true
	case "updated":
		return contractsorm.EventUpdated, true
	case "deleting":
		return contractsorm.EventDeleting, true
	case "deleted":
		return contractsorm.EventDeleted, true
	}

	return "", false
}
