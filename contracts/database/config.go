package database

import "database/sql/driver"

type Pool struct {
	Readers []Config
	Writers []Config
}

type Config struct {
	// Connector opens the pool for this connection when it is set. A driver module may supply one when
	// it targets a database the framework does not have a dialect for; otherwise the framework builds
	// the connection itself from the fields below.
	Connector    driver.Connector
	NameReplacer Replacer
	Charset      string
	Connection   string
	Dsn          string
	Database     string
	Driver       string
	Host         string
	Password     string
	Prefix       string
	Schema       string
	Sslmode      string
	Timezone     string
	Username     string
	Port         int
	NoLowerCase  bool
	Singular     bool
}

// Replacer replacer interface like strings.Replacer
type Replacer interface {
	Replace(name string) string
}
