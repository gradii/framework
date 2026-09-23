package mail

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A subject becomes a header, so a bare CR or LF in a rendered subject is
// header injection: everything after it is read as a new header. Keeping the
// first non-empty line drops the injected remainder. Template files end with a
// newline, so the flattening is the common case, not only the hostile one.
func TestSanitizeSubject(t *testing.T) {
	tests := map[string]string{
		"Your code":                              "Your code",
		"Your code\n":                            "Your code",
		"Your code\r\n":                          "Your code",
		"\n\nYour code":                          "Your code",
		"  Your code  ":                          "Your code",
		"Your code\nBcc: attacker@example.com":   "Your code",
		"Your code\r\nBcc: attacker@example.com": "Your code",
		"  \n  Your code  \n  second line":       "Your code",
		"":                                       "",
		"   ":                                    "",
		"\r\n":                                   "",
	}

	for input, want := range tests {
		assert.Equal(t, want, sanitizeSubject(input), "sanitizeSubject(%q)", input)
	}
}

func TestConvertMapHeadersToSlice(t *testing.T) {
	tests := []struct {
		name    string
		headers map[string]string
		want    []string
	}{
		{
			name:    "empty map",
			headers: map[string]string{},
			want:    []string{},
		},
		{
			name: "single header",
			headers: map[string]string{
				"Content-Type": "text/plain",
			},
			want: []string{"Content-Type: text/plain"},
		},
		{
			name: "multiple headers",
			headers: map[string]string{
				"Content-Type": "text/plain",
				"From":         "test@example.com",
				"To":           "recipient@example.com",
			},
			want: []string{
				"Content-Type: text/plain",
				"From: test@example.com",
				"To: recipient@example.com",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := convertMapHeadersToSlice(tt.headers)
			assert.ElementsMatch(t, tt.want, got, "convertMapHeadersToSlice() = %v, want %v", got, tt.want)
		})
	}
}

func TestConvertSliceHeadersToMap(t *testing.T) {
	tests := []struct {
		name    string
		headers []string
		want    map[string]string
	}{
		{
			name:    "empty slice",
			headers: []string{},
			want:    map[string]string{},
		},
		{
			name:    "single header",
			headers: []string{"Content-Type: text/plain"},
			want: map[string]string{
				"Content-Type": "text/plain",
			},
		},
		{
			name: "multiple headers",
			headers: []string{
				"Content-Type: text/plain",
				"From: test@example.com",
				"To: recipient@example.com",
			},
			want: map[string]string{
				"Content-Type": "text/plain",
				"From":         "test@example.com",
				"To":           "recipient@example.com",
			},
		},
		{
			name: "headers with extra spaces",
			headers: []string{
				"Content-Type : text/plain",
				"From : test@example.com",
				"To : recipient@example.com",
			},
			want: map[string]string{
				"Content-Type": "text/plain",
				"From":         "test@example.com",
				"To":           "recipient@example.com",
			},
		},
		{
			name: "invalid header format",
			headers: []string{
				"Content-Type: text/plain",
				"InvalidHeader",
				"To: recipient@example.com",
			},
			want: map[string]string{
				"Content-Type": "text/plain",
				"To":           "recipient@example.com",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := convertSliceHeadersToMap(tt.headers)
			assert.Equal(t, tt.want, got, "convertSliceHeadersToMap() = %v, want %v", got, tt.want)
		})
	}
}
