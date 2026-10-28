package driver

import (
	"context"
	sqlDriver "database/sql/driver"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gradii/framework/contracts/database"
	contractsdriver "github.com/gradii/framework/contracts/database/driver"
	querybuilder "github.com/gradii/framework/database/query"
	"github.com/gradii/framework/errors"
)

func driverDialect(name string) string {
	name = strings.NewReplacer(" ", "", "_", "", "-", "").Replace(strings.ToLower(name))
	switch name {
	case "postgresql", "pgsql", "pgx", "pq":
		return "postgres"
	case "mssql", "sqlsrv":
		return "sqlserver"
	case "sqlite3":
		return "sqlite"
	case "mariadb":
		return "mysql"
	default:
		return name
	}
}

// PurgeConnection removes one logical connection. Supply the driver's pool when its configured
// connection name differs from name; handles can call Connection.Purge without resolving it again.
// database/sql lets already leased connections finish their transactions while refusing new work.
func PurgeConnection(name string, pools ...database.Pool) error {
	return purgeConnection(namedConnectionIdentity(name, pools))
}

func purgeConnection(identity connectionIdentity) error {
	connectionToDBLock.Lock()
	cached, found := connectionToDB.LoadAndDelete(identity)
	reader, hasReader := readPools.LoadAndDelete(identity)
	connectionToDBLock.Unlock()
	var failures []error
	if found {
		connection := cached.(*cachedConnection)
		connection.instrument.Shutdown()
		if connection.db != nil {
			if err := connection.db.Close(); err != nil {
				failures = append(failures, err)
			}
		}
	}
	if hasReader {
		if err := reader.(cachedReader).pool.Close(); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (r *Connection) ServerVersion(ctx context.Context) (string, error) {
	if r.writer == nil || r.resources == nil {
		return "", errors.DatabaseConnectionIsNil
	}
	r.resources.versionMu.Lock()
	defer r.resources.versionMu.Unlock()
	if r.resources.version != "" {
		return r.resources.version, nil
	}
	var statement string
	if dialect, ok := r.dialect.(contractsdriver.DialectWithServerVersion); ok {
		statement = dialect.ServerVersionSQL()
	}
	if statement == "" {
		return "", errors.DatabaseDialectRequired.Args(r.driver)
	}
	if err := r.writer.QueryRowContext(ctx, statement).Scan(&r.resources.version); err != nil {
		return "", err
	}
	return r.resources.version, nil
}

// Escape produces a scalar SQL literal. Bound execution continues to use driver parameters.
func Escape(driver string, value any, binary bool) (string, error) {
	if querybuilder.CanonicalDialect(driver) != "" {
		return "", errors.DatabaseDialectRequired.Args(driver)
	}
	if value == nil {
		return "null", nil
	}
	reflected := reflect.ValueOf(value)
	if reflected.Kind() == reflect.Pointer && reflected.IsNil() {
		return "null", nil
	}
	if valuer, ok := value.(sqlDriver.Valuer); ok {
		scalar, err := valuer.Value()
		if err != nil {
			return "", err
		}
		return Escape(driver, scalar, binary)
	}
	if reflected.Kind() == reflect.Pointer {
		return Escape(driver, reflected.Elem().Interface(), binary)
	}
	if date, ok := value.(time.Time); ok {
		value = date.Format("2006-01-02 15:04:05")
		reflected = reflect.ValueOf(value)
	}
	if binary {
		var bytes []byte
		switch input := value.(type) {
		case []byte:
			bytes = input
		case string:
			bytes = []byte(input)
		default:
			return "", errors.DatabaseUnsupportedType.Args(fmt.Sprintf("%T", value), "string or []byte")
		}
		_ = bytes
		return "", errors.DatabaseEscapeBinaryNotSupported
	}
	switch reflected.Kind() {
	case reflect.Bool:
		if reflected.Bool() {
			return "1", nil
		}
		return "0", nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(reflected.Int(), 10), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(reflected.Uint(), 10), nil
	case reflect.Float32, reflect.Float64:
		number := reflected.Float()
		if math.IsNaN(number) || math.IsInf(number, 0) {
			return "", errors.DatabaseEscapeNonFinite
		}
		return strconv.FormatFloat(number, 'g', -1, reflected.Type().Bits()), nil
	}
	var text string
	switch input := value.(type) {
	case []byte:
		text = string(input)
	default:
		if reflected.Kind() != reflect.String {
			return "", errors.DatabaseUnsupportedType.Args(fmt.Sprintf("%T", value), "scalar or []byte")
		}
		text = reflected.String()
	}
	if !utf8.ValidString(text) {
		return "", errors.DatabaseEscapeInvalidUTF8
	}
	if strings.IndexByte(text, 0) >= 0 {
		return "", errors.DatabaseEscapeNullByte
	}
	return "'" + strings.ReplaceAll(text, "'", "''") + "'", nil
}
