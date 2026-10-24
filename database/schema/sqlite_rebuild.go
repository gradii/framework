package schema

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/goravel/framework/contracts/database/driver"
	"github.com/goravel/framework/errors"
)

type sqliteToken struct {
	start, end         int
	value              string
	identifier, quoted bool
}

type sqliteColumnConstraint struct {
	start, end int
	kind, name string
}

func sqliteInlineConstraints(definition string) []sqliteColumnConstraint {
	tokens := sqliteTokens(definition)
	var constraints []sqliteColumnConstraint
	depth := 0
	for i := 1; i < len(tokens); i++ {
		token := tokens[i]
		if token.quoted {
			continue
		}
		if token.value == "(" {
			depth++
			continue
		}
		if token.value == ")" {
			depth--
			continue
		}
		if depth != 0 {
			continue
		}
		kind := strings.ToLower(token.value)
		start, name := token.start, ""
		if i >= 2 && !tokens[i-2].quoted && strings.EqualFold(tokens[i-2].value, "constraint") {
			start, name = tokens[i-2].start, tokens[i-1].value
		}
		end := i + 1
		switch kind {
		case "primary":
			if end >= len(tokens) || !strings.EqualFold(tokens[end].value, "key") {
				continue
			}
			end++
			for end < len(tokens) && slicesContains([]string{"asc", "desc", "autoincrement"}, strings.ToLower(tokens[end].value)) {
				end++
			}
			if end+2 < len(tokens) && strings.EqualFold(tokens[end].value, "on") && strings.EqualFold(tokens[end+1].value, "conflict") {
				end += 3
			}
			if end < len(tokens) && strings.EqualFold(tokens[end].value, "autoincrement") {
				end++
			}
		case "unique":
			if end+2 < len(tokens) && strings.EqualFold(tokens[end].value, "on") && strings.EqualFold(tokens[end+1].value, "conflict") {
				end += 3
			}
		case "check":
			if end >= len(tokens) || tokens[end].value != "(" {
				continue
			}
			nesting := 0
			for end < len(tokens) {
				if !tokens[end].quoted {
					if tokens[end].value == "(" {
						nesting++
					}
					if tokens[end].value == ")" {
						nesting--
					}
				}
				end++
				if nesting == 0 {
					break
				}
			}
		case "references":
			if end >= len(tokens) {
				continue
			}
			end++
			if end+1 < len(tokens) && tokens[end].value == "." {
				end += 2
			}
			if end < len(tokens) && tokens[end].value == "(" {
				for end < len(tokens) && tokens[end].value != ")" {
					end++
				}
				if end < len(tokens) {
					end++
				}
			}
		referenceLoop:
			for end < len(tokens) {
				word := strings.ToLower(tokens[end].value)
				switch {
				case word == "on" && end+2 < len(tokens):
					end += 2
					action := strings.ToLower(tokens[end].value)
					end++
					if (action == "set" || action == "no") && end < len(tokens) {
						end++
					}
				case word == "match" && end+1 < len(tokens):
					end += 2
				case word == "not" && end+1 < len(tokens) && strings.EqualFold(tokens[end+1].value, "deferrable"):
					end += 2
				case word == "deferrable":
					end++
				case word == "initially" && end+1 < len(tokens):
					end += 2
				default:
					break referenceLoop
				}
			}
		default:
			continue
		}
		if end > len(tokens) {
			end = len(tokens)
		}
		if end > i {
			constraints = append(constraints, sqliteColumnConstraint{start: start, end: tokens[end-1].end, kind: kind, name: name})
			i = end - 1
		}
	}
	return constraints
}
func sqliteRemoveConstraint(definition string, constraint sqliteColumnConstraint) string {
	return strings.TrimSpace(definition[:constraint.start] + " " + definition[constraint.end:])
}

// sqliteTokens keeps string literals and comments separate from identifiers during DDL rewriting.
func sqliteTokens(sql string) []sqliteToken {
	var result []sqliteToken
	for i := 0; i < len(sql); {
		character, width := utf8.DecodeRuneInString(sql[i:])
		if unicode.IsSpace(character) {
			i += width
			continue
		}
		if i+1 < len(sql) && sql[i:i+2] == "--" {
			for i < len(sql) && sql[i] != '\n' {
				i++
			}
			continue
		}
		if i+1 < len(sql) && sql[i:i+2] == "/*" {
			if end := strings.Index(sql[i+2:], "*/"); end >= 0 {
				i += end + 4
			} else {
				i = len(sql)
			}
			continue
		}
		start := i
		if sql[i] == '\'' || sql[i] == '"' || sql[i] == '`' || sql[i] == '[' {
			quote, close := sql[i], sql[i]
			if close == '[' {
				close = ']'
			}
			i++
			var value strings.Builder
			for i < len(sql) {
				if sql[i] == close {
					if i+1 < len(sql) && sql[i+1] == close {
						value.WriteByte(close)
						i += 2
						continue
					}
					i++
					break
				}
				value.WriteByte(sql[i])
				i++
			}
			result = append(result, sqliteToken{start: start, end: i, value: value.String(), identifier: quote != '\'', quoted: true})
			continue
		}
		if sqliteIdentifierRune(character) {
			i += width
			for i < len(sql) {
				character, width = utf8.DecodeRuneInString(sql[i:])
				if unicode.IsSpace(character) || (!sqliteIdentifierRune(character) && !unicode.IsDigit(character)) {
					break
				}
				i += width
			}
			result = append(result, sqliteToken{start: start, end: i, value: sql[start:i], identifier: true})
			continue
		}
		i++
		result = append(result, sqliteToken{start: start, end: i, value: sql[start:i]})
	}
	return result
}
func sqliteIdentifierRune(character rune) bool {
	return unicode.IsLetter(character) || character >= utf8.RuneSelf || character == '_' || character == '$'
}
func sqliteDefinitionParts(sql string) ([]string, string, error) {
	depth, start := 0, -1
	var parts []string
	for _, token := range sqliteTokens(sql) {
		if token.quoted {
			continue
		}
		switch token.value {
		case "(":
			depth++
			if depth == 1 {
				start = token.end
			}
		case ")":
			depth--
			if depth == 0 && start >= 0 {
				parts = append(parts, strings.TrimSpace(sql[start:token.start]))
				return parts, sql[token.end:], nil
			}
		case ",":
			if depth == 1 {
				parts = append(parts, strings.TrimSpace(sql[start:token.start]))
				start = token.end
			}
		}
	}
	return nil, "", errors.SchemaSQLiteDefinitionInvalid
}
func sqliteConstraint(part string) (kind, name string, columns []string) {
	tokens := sqliteTokens(part)
	if len(tokens) == 0 || tokens[0].quoted {
		return "", "", nil
	}
	position := 0
	if strings.EqualFold(tokens[0].value, "constraint") {
		if len(tokens) < 3 {
			return "", "", nil
		}
		name, position = tokens[1].value, 2
	}
	kind = strings.ToLower(tokens[position].value)
	if !slicesContains([]string{"primary", "foreign", "unique", "check"}, kind) {
		return "", "", nil
	}
	depth := 0
	for _, token := range tokens[position+1:] {
		if token.value == "(" && !token.quoted {
			depth++
			continue
		}
		if token.value == ")" && !token.quoted {
			depth--
			if depth == 0 {
				break
			}
		}
		if depth == 1 && token.identifier {
			columns = append(columns, token.value)
		}
	}
	return kind, name, columns
}
func slicesContains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
func sqliteRename(sql string, renames map[string]string) string {
	tokens := sqliteTokens(sql)
	var output strings.Builder
	start := 0
	for i, token := range tokens {
		if !token.identifier {
			continue
		}
		replacement, ok := renames[strings.ToLower(token.value)]
		if !ok {
			continue
		}
		// An unquoted function name is not a column reference.
		if !token.quoted && i+1 < len(tokens) && tokens[i+1].value == "(" {
			continue
		}
		output.WriteString(sql[start:token.start])
		output.WriteString(quoteSchemaIdentifier(replacement))
		start = token.end
	}
	output.WriteString(sql[start:])
	return output.String()
}
func sqliteReferences(sql string, names map[string]bool) bool {
	for _, token := range sqliteTokens(sql) {
		if token.identifier && names[strings.ToLower(token.value)] {
			return true
		}
	}
	return false
}
func (b *Blueprint) sqliteRebuild() bool {
	if b.isCreate() {
		return false
	}
	for _, column := range b.columns {
		if column.change {
			return true
		}
	}
	for _, command := range b.commands {
		switch command.Name {
		case CommandDropColumn, CommandForeign, CommandDropForeign, CommandPrimary, CommandDropPrimary:
			return true
		}
	}
	return false
}

func (r *Schema) compileSQLiteRebuild(blueprint *Blueprint, grammar driver.Grammar) ([]string, error) {
	for _, command := range blueprint.commands {
		if command.Name == CommandDropForeign && len(command.Columns) == 0 {
			return nil, errors.SchemaDropForeignByNameNotSupported
		}
	}
	namespace, table, err := r.metadataTable(blueprint.table)
	if err != nil {
		return nil, err
	}
	namespace = sqliteSchemaName(namespace)
	records, err := r.selectMetadata("select sql from "+quoteSchemaIdentifier(namespace)+".sqlite_master where type = 'table' and name = ?", table)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, errors.SchemaTableNotFound.Args(blueprint.table)
	}
	create, _ := records[0]["sql"].(string)
	parts, suffix, err := sqliteDefinitionParts(create)
	if err != nil {
		return nil, err
	}
	columns, err := r.GetColumnsMetadata(blueprint.table)
	if err != nil {
		return nil, err
	}
	objects, err := r.selectMetadata("select type, name, sql from "+quoteSchemaIdentifier(namespace)+".sqlite_master where tbl_name = ? and type in ('index', 'trigger') and sql is not null order by type, name", table)
	if err != nil {
		return nil, err
	}

	dropped := map[string]bool{}
	renames := map[string]string{}
	changed := map[string]*ColumnDefinition{}
	removedIndexes := map[string]bool{}
	dropPrimary := false
	finalTable := table
	var addedForeign []*driver.Command
	var primary []string
	for _, column := range blueprint.columns {
		if column.change {
			changed[strings.ToLower(column.GetName())] = column
		}
	}
	for _, command := range blueprint.commands {
		switch command.Name {
		case CommandDropColumn:
			for _, column := range command.Columns {
				dropped[strings.ToLower(column)] = true
			}
		case CommandRenameColumn:
			renames[strings.ToLower(command.From)] = command.To
		case CommandRename:
			finalTable = r.prefix + command.To
		case CommandDropIndex, CommandDropUnique, CommandDropFullText, "dropSpatialIndex":
			removedIndexes[strings.ToLower(command.Index)] = true
		case CommandDropPrimary:
			dropPrimary = true
		case CommandPrimary:
			dropPrimary = true
			primary = command.Columns
		case CommandForeign:
			addedForeign = append(addedForeign, command)
		}
	}

	var definitions, constraints []string
	existing := make(map[string]bool, len(columns))
	for _, column := range columns {
		existing[strings.ToLower(column.Name)] = true
	}
	for name := range changed {
		if !existing[name] {
			return nil, errors.SchemaColumnNotFound.Args(name, blueprint.table)
		}
	}
	for name := range dropped {
		if !existing[name] {
			return nil, errors.SchemaColumnNotFound.Args(name, blueprint.table)
		}
	}
	for name := range renames {
		if !existing[name] {
			return nil, errors.SchemaColumnNotFound.Args(name, blueprint.table)
		}
	}
	for _, part := range parts {
		kind, name, localColumns := sqliteConstraint(part)
		if kind != "" {
			remove := kind == "primary" && dropPrimary
			for _, column := range localColumns {
				if dropped[strings.ToLower(column)] {
					remove = true
				}
			}
			if kind == "check" && sqliteReferences(part, dropped) {
				remove = true
			}
			for _, command := range blueprint.commands {
				if command.Name != CommandDropForeign || kind != "foreign" {
					continue
				}
				if command.Index != "" && strings.EqualFold(command.Index, name) {
					remove = true
				}
				if len(command.Columns) > 0 && equalDDLColumns(command.Columns, localColumns) {
					remove = true
				}
			}
			if !remove {
				if kind == "foreign" {
					// Foreign target column names belong to the referenced table.
					tokens := sqliteTokens(part)
					split := len(part)
					for _, token := range tokens {
						if !token.quoted && strings.EqualFold(token.value, "references") {
							split = token.start
							break
						}
					}
					part = sqliteRename(part[:split], renames) + part[split:]
				} else {
					part = sqliteRename(part, renames)
				}
				constraints = append(constraints, part)
			}
			continue
		}
		tokens := sqliteTokens(part)
		if len(tokens) == 0 {
			continue
		}
		name = tokens[0].value
		if dropped[strings.ToLower(name)] {
			continue
		}
		inline := sqliteInlineConstraints(part)
		remove := make(map[int]bool)
		for i, constraint := range inline {
			if constraint.kind == "primary" && dropPrimary {
				remove[i] = true
			}
			if constraint.kind == "check" && sqliteReferences(part[constraint.start:constraint.end], dropped) {
				remove[i] = true
			}
			if constraint.kind == "references" {
				for _, command := range blueprint.commands {
					if command.Name != CommandDropForeign {
						continue
					}
					if equalDDLColumns(command.Columns, []string{name}) || (constraint.name != "" && strings.EqualFold(command.Index, constraint.name)) {
						remove[i] = true
					}
				}
			}
		}
		if column, exists := changed[strings.ToLower(name)]; exists {
			replacement := *column
			replacement.change = false
			if renamed, ok := renames[strings.ToLower(name)]; ok {
				replacement.name = &renamed
			}
			definition, err := r.ddlColumn(grammar, blueprint, &replacement)
			if err != nil {
				return nil, err
			}
			present := make(map[string]bool)
			for _, constraint := range sqliteInlineConstraints(definition) {
				present[constraint.kind] = true
			}
			for i, constraint := range inline {
				if remove[i] || present[constraint.kind] {
					continue
				}
				retained := part[constraint.start:constraint.end]
				if constraint.kind == "check" {
					retained = sqliteRename(retained, renames)
				}
				definition += " " + retained
			}
			part = definition
		} else {
			for i := len(inline) - 1; i >= 0; i-- {
				if remove[i] {
					part = sqliteRemoveConstraint(part, inline[i])
				}
			}
			part = sqliteRename(part, renames)
		}

		definitions = append(definitions, part)
	}
	for _, column := range blueprint.columns {
		if column.change {
			continue
		}
		definition, err := r.ddlColumn(grammar, blueprint, column)
		if err != nil {
			return nil, err
		}
		definitions = append(definitions, definition)
	}
	if len(primary) > 0 {
		constraints = append(constraints, "primary key ("+r.ddlWrap().Columnize(primary)+")")
	}
	for _, command := range addedForeign {
		constraints = append(constraints, r.sqliteForeignDefinition(command))
	}
	definitions = append(definitions, constraints...)
	if len(definitions) == 0 {
		return nil, errors.SchemaSQLiteDefinitionInvalid
	}

	var sourceColumns, targetColumns []string
	for _, column := range columns {
		if dropped[strings.ToLower(column.Name)] {
			continue
		}
		generated := column.Generation != nil
		if replacement := changed[strings.ToLower(column.Name)]; replacement != nil {
			generated = replacement.storedAs != nil || replacement.virtualAs != nil
		}
		if generated {
			continue
		}
		target := column.Name
		if renamed, ok := renames[strings.ToLower(column.Name)]; ok {
			target = renamed
		}
		sourceColumns = append(sourceColumns, quoteSchemaIdentifier(column.Name))
		targetColumns = append(targetColumns, quoteSchemaIdentifier(target))
	}
	if len(sourceColumns) == 0 {
		return nil, errors.SchemaSQLiteNoWritableColumns
	}
	temporary := "__temp__" + table
	qualified := func(name string) string { return quoteSchemaIdentifier(namespace) + "." + quoteSchemaIdentifier(name) }
	statements := []string{
		"create table " + qualified(temporary) + " (" + strings.Join(definitions, ", ") + ")" + suffix,
		"insert into " + qualified(temporary) + " (" + strings.Join(targetColumns, ", ") + ") select " + strings.Join(sourceColumns, ", ") + " from " + qualified(table),
		"drop table " + qualified(table),
		"alter table " + qualified(temporary) + " rename to " + quoteSchemaIdentifier(finalTable),
	}
	for _, object := range objects {
		name, _ := object["name"].(string)
		sql, _ := object["sql"].(string)
		kind, _ := object["type"].(string)
		if removedIndexes[strings.ToLower(name)] {
			continue
		}
		tokens := sqliteTokens(sql)
		if kind == "index" {
			open := -1
			for _, token := range tokens {
				if !token.quoted && token.value == "(" {
					open = token.start
					break
				}
			}
			if open < 0 || sqliteReferences(sql[open:], dropped) {
				continue
			}
			unique := ""
			if len(tokens) > 1 && strings.EqualFold(tokens[1].value, "unique") {
				unique = "unique "
			}
			statements = append(statements, "create "+unique+"index "+qualified(name)+" on "+quoteSchemaIdentifier(finalTable)+" "+sqliteRename(sql[open:], renames))
		} else {
			statement, keep := sqliteTrigger(sql, table, finalTable, renames, dropped)
			if keep {
				statements = append(statements, statement)
			}
		}
	}
	for _, command := range blueprint.commands {
		switch command.Name {
		case CommandIndex:
			statements = append(statements, grammar.CompileIndex(blueprint, command))
		case CommandUnique:
			statements = append(statements, grammar.CompileUnique(blueprint, command))
		case "rawIndex":
			compiled, _, err := r.compileDDLCommand(blueprint, grammar, command)
			if err != nil {
				return nil, err
			}
			statements = append(statements, compiled...)
		}
	}
	return statements, nil
}
func equalDDLColumns(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !strings.EqualFold(a[i], b[i]) {
			return false
		}
	}
	return true
}

// Trigger NEW/OLD references and UPDATE OF lists refer to this table; other target
// tables and string literals retain their own identifiers.
func sqliteTrigger(sql, oldTable, newTable string, renames map[string]string, dropped map[string]bool) (string, bool) {
	tokens := sqliteTokens(sql)
	type replacement struct {
		start, end int
		value      string
	}
	var changes []replacement
	beforeOn, updateOf := true, false
	for i, token := range tokens {
		if !token.identifier {
			continue
		}
		local := i >= 2 && tokens[i-1].value == "." && (strings.EqualFold(tokens[i-2].value, "new") || strings.EqualFold(tokens[i-2].value, "old"))
		if beforeOn && strings.EqualFold(token.value, "of") {
			updateOf = true
			continue
		}
		if beforeOn && strings.EqualFold(token.value, "on") {
			beforeOn, updateOf = false, false
			if i+1 < len(tokens) && oldTable != newTable {
				next := tokens[i+1]
				changes = append(changes, replacement{next.start, next.end, quoteSchemaIdentifier(newTable)})
			}
			continue
		}
		if local || updateOf {
			if dropped[strings.ToLower(token.value)] {
				return "", false
			}
			if renamed, ok := renames[strings.ToLower(token.value)]; ok {
				changes = append(changes, replacement{token.start, token.end, quoteSchemaIdentifier(renamed)})
			}
		}
	}
	var out strings.Builder
	start := 0
	for _, change := range changes {
		if change.start < start {
			continue
		}
		out.WriteString(sql[start:change.start])
		out.WriteString(change.value)
		start = change.end
	}
	out.WriteString(sql[start:])
	return out.String(), true
}
