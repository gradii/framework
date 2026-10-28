# JSON database values

`JSON` and `JSONMap` implement Go's `database/sql.Scanner` and
`database/sql/driver.Valuer`. They do not depend on GORM. The same interfaces can
be implemented by application types:

```go
package models

import (
    "database/sql/driver"
    "encoding/json"

    "github.com/gradii/framework/database/orm"
    "github.com/gradii/framework/database/types"
)

type User struct {
    orm.Model
    Options types.JSONMap `gorm:"column:options;type:json" json:"options"`
    Profile *UserData     `gorm:"column:profile;type:json" json:"profile"`
}

func (User) TableName() string { return "users" }

type UserData struct {
    Name string `json:"name"`
    Age  int    `json:"age"`
}

func (value *UserData) Value() (driver.Value, error) {
    if value == nil {
        return nil, nil // SQL NULL; json.Marshal(nil) would produce JSON null.
    }
    return json.Marshal(value)
}

func (value *UserData) Scan(source any) error {
    var document types.JSON
    if err := document.Scan(source); err != nil {
        return err
    }
    var next UserData
    if document != nil {
        if err := json.Unmarshal(document, &next); err != nil {
            return err
        }
    }
    *value = next
    return nil
}
```

The temporary destination replaces old fields only after successful decoding.
Scanners receive the driver's original `string`, `[]byte`, or nil. A scanner
that previously accepted only bytes must also handle strings. Invalid JSON and
unsupported source types return errors; empty text is not SQL NULL.

| Value | SQL representation |
| --- | --- |
| `types.JSON(nil)` | SQL NULL |
| `types.JSON("null")` | JSON null |
| `types.JSON("[]")` | Empty JSON array |
| `types.JSONMap(nil)` | SQL NULL |
| `types.JSONMap{}` | Empty JSON object |

`JSONMap.Scan` maps both SQL NULL and JSON null to a nil map. Use `JSON` when
that distinction matters. JSONMap accepts objects and null; other JSON values
are rejected. Both scanners own any retained bytes and preserve their old value
on errors. `Value` returns JSON text as a string for portability.

Whole-column Create, Save and Update preserve Valuers as single SQL parameters.
ORM fields whose `Value` method has a pointer receiver work in both pointer and
value models. Passing an arbitrary value directly to the DB API still follows
Go's method sets: pass `*T` when only `*T` implements Valuer.

`type:json` / `type:jsonb` declare JSON storage. For those model fields, a Valuer's
byte result is validated and bound as UTF-8 JSON text, preventing SQL Server from
interpreting the bytes as a UTF-16 binary-to-text conversion. This applies to
Create, Save and updates associated with that model. The tags do not serialize
arbitrary structs. A DB query without model metadata retains native parameter
types; use these JSON types or a Valuer returning a JSON string for portability.
`serializer:json` is not a serializer implementation in this framework. A
redundant serializer tag is accepted on a type implementing both standard
interfaces; otherwise model parsing reports an error. Prefer omitting it.

JSON path updates and containment are separate dialect operations. They respect
Valuer output while applying each database's JSON rules: valid JSON text is
interpreted as a document, and ordinary SQL text as a string scalar. A JSON
type's Value method is responsible for rejecting invalid documents. SQL Server supports
scalar containment and the existing flat scalar list extension; object and
nested-list containment return errors. An interface signature accepting `any`
does not imply that every JSON shape is supported by every database.

The PostgreSQL connector keeps its simple protocol and does not cache a prepared
plan. Explicit JSON model bindings provide JSON text; raw bytes passed directly
to the DB API retain pgx's bytea behavior. A supplied Connector retains
responsibility for its protocol mode.
