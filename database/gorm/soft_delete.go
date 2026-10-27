package gorm

import (
	"reflect"

	basedatabase "github.com/goravel/framework/support/database"
)

// Soft-delete decisions shared by the delete path (delete.go) and the compiled read path (sql.go).
//
// The predicates themselves are built by the compiled twins — applySoftDelete (sql.go) for reads and
// compileSoftDeleteUpdate (delete.go) for writes — so nothing here touches a GORM statement. GORM's
// own soft-delete clause deliberately does not apply: the framework models carry
// support/database.DeletedAt, which implements none of the clause interfaces GORM recognises. Both
// helpers below are gated on the model actually declaring that type, which keeps GORM's behaviour
// untouched for models that have not been migrated yet.

// usesFrameworkSoftDelete reports whether the model's soft-delete field is the framework's own
// type, and therefore needs the predicate built by the compiled path rather than by GORM.
func (r *Query) usesFrameworkSoftDelete() bool {
	return usesFrameworkSoftDeleteModel(r.parentModel())
}

// usesFrameworkSoftDeleteModel is the model-argument twin of usesFrameworkSoftDelete, for callers
// that hold the related model (an existence subquery's related model, say) rather than the query's
// own. It reports whether model declares the framework's own soft-delete type - the only kind the
// compiled path builds a predicate for.
func usesFrameworkSoftDeleteModel(model any) bool {
	if model == nil {
		return false
	}

	fieldType, ok := softDeleteFieldType(model)

	return ok && fieldType == reflect.TypeOf(basedatabase.DeletedAt{})
}
