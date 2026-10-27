package grammars

import (
	"strings"

	"github.com/gradii/framework/database/query/core"
)

// SetEscaper supplies the connection's literal escaping, as Laravel's Grammar::escape does.
// SQL previews use the framework's value formatter when no connection escaper was supplied.
func (g *BaseGrammar) SetEscaper(escape func(any, bool) string) { g.escape = escape }

// SubstituteBindingsIntoRawSQL ports Grammar::substituteBindingsIntoRawSql. Escaped quotes and
// escaped question marks do not consume bindings, nor do question marks inside string literals.
func (g *BaseGrammar) SubstituteBindingsIntoRawSQL(sql string, bindings []any) string {
	escaped := make([]string, len(bindings))
	for i, value := range bindings {
		if g.escape != nil {
			_, binary := value.([]byte)
			escaped[i] = g.escape(value, binary)
		} else {
			escaped[i] = core.RenderLiteral(value)
		}
	}

	var output strings.Builder
	insideLiteral := false
	binding := 0
	for i := 0; i < len(sql); i++ {
		if i+1 < len(sql) {
			pair := sql[i : i+2]
			if pair == "\\'" || pair == "''" || pair == "??" {
				output.WriteString(pair)
				i++
				continue
			}
		}
		if sql[i] == '\'' {
			insideLiteral = !insideLiteral
		} else if sql[i] == '?' && !insideLiteral && binding < len(escaped) {
			output.WriteString(escaped[binding])
			binding++
			continue
		}
		output.WriteByte(sql[i])
	}
	return output.String()
}

// SubstituteBindingsIntoRawSQL restores PostgreSQL's escaped ? operators after substitution.
func (g *PostgresGrammar) SubstituteBindingsIntoRawSQL(sql string, bindings []any) string {
	result := g.BaseGrammar.SubstituteBindingsIntoRawSQL(sql, bindings)
	for _, operator := range postgresOperators {
		if strings.Contains(operator, "?") {
			result = strings.ReplaceAll(result, strings.ReplaceAll(operator, "?", "??"), operator)
		}
	}
	return result
}
