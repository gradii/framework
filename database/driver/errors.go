package driver

import (
	"context"
	"database/sql/driver"
	"io"
	"reflect"
	"strings"
	"syscall"

	"github.com/gradii/framework/errors"
)

// Laravel's detectors recognize database failures by SQLSTATE and driver messages.
// Native Go driver codes are checked before the upstream message corpus.
func CausedByConcurrencyError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return inspectDatabaseErrors(err, func(err error) bool {
		if state, ok := err.(interface{ SQLState() string }); ok {
			if code := state.SQLState(); code == "40001" || code == "40P01" {
				return true
			}
		}
		value := reflect.Indirect(reflect.ValueOf(err))
		if value.IsValid() && value.Kind() == reflect.Struct {
			pkg := value.Type().PkgPath()
			if strings.Contains(pkg, "go-sql-driver/mysql") || strings.Contains(pkg, "go-mssqldb") {
				number := value.FieldByName("Number")
				if number.IsValid() {
					var code int64
					if number.CanInt() {
						code = number.Int()
					} else if number.CanUint() {
						code = int64(number.Uint())
					}
					if code == 1205 || code == 1213 || code == 3960 {
						return true
					}
				}
			}
		}
		return containsDatabaseMessage(err.Error(), concurrencyMessages)
	})
}

func CausedByLostConnection(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, driver.ErrBadConn) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	return inspectDatabaseErrors(err, func(err error) bool {
		if state, ok := err.(interface{ SQLState() string }); ok && strings.HasPrefix(state.SQLState(), "08") {
			return true
		}
		return containsDatabaseMessage(err.Error(), lostConnectionMessages)
	})
}

func inspectDatabaseErrors(err error, match func(error) bool) bool {
	if err == nil {
		return false
	}
	if match(err) {
		return true
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, child := range joined.Unwrap() {
			if inspectDatabaseErrors(child, match) {
				return true
			}
		}
		return false
	}
	return inspectDatabaseErrors(errors.Unwrap(err), match)
}
func containsDatabaseMessage(message string, candidates []string) bool {
	for _, candidate := range candidates {
		if strings.Contains(message, candidate) {
			return true
		}
	}
	return false
}

var concurrencyMessages = []string{
	"Deadlock found when trying to get lock",
	"deadlock detected",
	"The database file is locked",
	"database is locked",
	"database table is locked",
	"A table in the database is locked",
	"has been chosen as the deadlock victim",
	"Lock wait timeout exceeded; try restarting transaction",
	"WSREP detected deadlock/conflict and aborted the transaction. Try restarting the transaction",
	"Record has changed since last read in table",
}

var lostConnectionMessages = []string{
	"server has gone away",
	"Server has gone away",
	"no connection to the server",
	"Lost connection",
	"is dead or not enabled",
	"Error while sending",
	"decryption failed or bad record mac",
	"server closed the connection unexpectedly",
	"SSL connection has been closed unexpectedly",
	"Error writing data to the connection",
	"Resource deadlock avoided",
	"Transaction() on null",
	"child connection forced to terminate due to client_idle_limit",
	"query_wait_timeout",
	"reset by peer",
	"Physical connection is not usable",
	"TCP Provider: Error code 0x68",
	"ORA-03114",
	"Packets out of order. Expected",
	"Adaptive Server connection failed",
	"Communication link failure",
	"connection is no longer usable",
	"Login timeout expired",
	"SQLSTATE[HY000] [2002] Connection refused",
	"running with the --read-only option so it cannot execute this statement",
	"The connection is broken and recovery is not possible. The connection is marked by the client driver as unrecoverable. No attempt was made to restore the connection.",
	"SQLSTATE[HY000] [2002] php_network_getaddresses: getaddrinfo failed: Try again",
	"SQLSTATE[HY000] [2002] php_network_getaddresses: getaddrinfo failed: Name or service not known",
	"SQLSTATE[HY000] [2002] php_network_getaddresses: getaddrinfo for",
	"SQLSTATE[HY000]: General error: 7 SSL SYSCALL error: EOF detected",
	"SSL error: unexpected eof",
	"SQLSTATE[HY000] [2002] Connection timed out",
	"SSL: Connection timed out",
	"SQLSTATE[HY000]: General error: 1105 The last transaction was aborted due to Seamless Scaling. Please retry.",
	"Temporary failure in name resolution",
	"SQLSTATE[08S01]: Communication link failure",
	"SQLSTATE[08006] [7] could not connect to server: Connection refused Is the server running on host",
	"SQLSTATE[HY000]: General error: 7 SSL SYSCALL error: No route to host",
	"The client was disconnected by the server because of inactivity. See wait_timeout and interactive_timeout for configuring this behavior.",
	"SQLSTATE[08006] [7] could not translate host name",
	"TCP Provider: Error code 0x274C",
	"SQLSTATE[HY000] [2002] No such file or directory",
	"SSL: Operation timed out",
	"Reason: Server is in script upgrade mode. Only administrator can connect at this time.",
	"Unknown $curl_error_code: 77",
	"SSL: Handshake timed out",
	"SSL error: sslv3 alert unexpected message",
	"unrecognized SSL error code:",
	"SQLSTATE[HY000] [1045] Access denied for user",
	"SQLSTATE[HY000] [2002] No connection could be made because the target machine actively refused it",
	"SQLSTATE[HY000] [2002] A connection attempt failed because the connected party did not properly respond after a period of time, or established connection failed because connected host has failed to respond",
	"SQLSTATE[HY000] [2002] Network is unreachable",
	"SQLSTATE[HY000] [2002] The requested address is not valid in its context",
	"SQLSTATE[HY000] [2002] A socket operation was attempted to an unreachable network",
	"SQLSTATE[HY000] [2002] Operation now in progress",
	"SQLSTATE[HY000] [2002] Operation in progress",
	"SQLSTATE[HY000]: General error: 3989",
	"went away",
	"No such file or directory",
	"server is shutting down",
	"failed to connect to",
	"Channel connection is closed",
	"Connection lost",
	"Broken pipe",
	"SQLSTATE[25006]: Read only sql transaction: 7",
	"vtgate connection error: no healthy endpoints",
	"primary is not serving, there may be a reparent operation in progress",
	"current keyspace is being resharded",
	"no healthy tablet available",
	"transaction pool connection limit exceeded",
	"SSL operation failed with code 5",
}
