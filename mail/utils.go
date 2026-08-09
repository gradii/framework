package mail

import "strings"

func convertMapHeadersToSlice(headers map[string]string) []string {
	var slice []string
	for key, value := range headers {
		slice = append(slice, key+": "+value)
	}
	return slice
}

// sanitizeSubject flattens a rendered subject to a single header-safe line.
// A subject is written as a mail header, so an embedded CR or LF would start
// another header; the first non-empty line is kept and the rest dropped.
func sanitizeSubject(subject string) string {
	for line := range strings.SplitSeq(subject, "\n") {
		if trimmed := strings.TrimSpace(strings.ReplaceAll(line, "\r", "")); trimmed != "" {
			return trimmed
		}
	}

	return ""
}

func convertSliceHeadersToMap(headers []string) map[string]string {
	mapHeaders := make(map[string]string)
	for _, header := range headers {
		parts := strings.Split(header, ":")
		if len(parts) == 2 {
			mapHeaders[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	return mapHeaders
}
