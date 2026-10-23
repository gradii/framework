package processors

import (
	"fmt"
	"strconv"
)

func columnString(value any) string {
	if value == nil {
		return ""
	}
	if bytes, ok := value.([]byte); ok {
		return string(bytes)
	}
	return fmt.Sprint(value)
}

// Metadata arrives as database scalars; preserve PHP's distinction between "0" and other strings.
func columnBool(value any) bool {
	if value == nil {
		return false
	}
	if boolean, ok := value.(bool); ok {
		return boolean
	}
	text := columnString(value)
	return text != "" && text != "0"
}

func columnInt(value any) int {
	if boolean, ok := value.(bool); ok && boolean {
		return 1
	}
	integer, _ := strconv.Atoi(columnString(value))
	return integer
}

func nullableComment(value any) any {
	if columnBool(value) {
		return value
	}
	return nil
}
