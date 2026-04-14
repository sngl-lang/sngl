package lsp

import (
	"sort"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
)

type rawToken struct {
	line, col, length int
	tokenType         uint32
}

func computeSemanticTokens(content string, doc *ast.Document) []uint32 {
	var tokens []rawToken
	lines := strings.Split(content, "\n")

	// Built-in type names
	builtinTypes := map[string]bool{
		"int": true, "float": true, "bool": true, "string": true,
		"dyn": true, "date": true, "color": true, "measurement": true,
		"component": true,
	}

	// TODO: Restore stdlib component names once v2 has stdlib loading
	stdlibNames := map[string]bool{}

	// User component names
	userCompNames := map[string]bool{}
	if doc != nil {
		for _, s := range doc.Stmts {
			if c, ok := s.(*ast.ComponentDecl); ok {
				userCompNames[c.Name] = true
			}
		}
	}

	// Scan each line for tokens
	for lineIdx, line := range lines {
		lineNum := lineIdx // 0-based for LSP

		// Find keywords, identifiers, and @ prefixed names
		i := 0
		for i < len(line) {
			ch := line[i]

			// Skip whitespace
			if ch == ' ' || ch == '\t' || ch == '\r' {
				i++
				continue
			}

			// Skip comments
			if i+1 < len(line) && line[i] == '/' && line[i+1] == '/' {
				break
			}

			// @ prefix → event
			if ch == '@' && i+1 < len(line) && isIdentStart(line[i+1]) {
				start := i + 1
				end := start
				for end < len(line) && isIdentChar(line[end]) {
					end++
				}
				tokens = append(tokens, rawToken{lineNum, i, end - i, stEvent})
				i = end
				continue
			}

			// Identifier-like token
			if isIdentStart(ch) {
				start := i
				for i < len(line) && isIdentChar(line[i]) {
					i++
				}
				word := line[start:i]

				// Check if it's a namespace prefix (word followed by .)
				if i < len(line) && line[i] == '.' {
					// Could be a qualified name: ns.Name
					tokens = append(tokens, rawToken{lineNum, start, len(word), stNamespace})
					continue
				}

				// Component names (stdlib or user-defined)
				if stdlibNames[word] || userCompNames[word] {
					// Check context: is this a component usage (not inside a string, not a keyword position)?
					// Simple heuristic: if the previous non-whitespace char is { or ; or start-of-line
					// and it's not a keyword, treat as component
					prevChar := prevNonSpace(line, start)
					if prevChar == '{' || prevChar == 0 || prevChar == ';' || prevChar == '/' {
						tokens = append(tokens, rawToken{lineNum, start, len(word), stClass})
						continue
					}
				}

				// Built-in types
				if builtinTypes[word] {
					tokens = append(tokens, rawToken{lineNum, start, len(word), stType})
					continue
				}

				// Struct/enum types (capitalized user types)
				if doc != nil {
					for _, stmt := range doc.Stmts {
						if sd, ok := stmt.(*ast.StructDef); ok && sd.Name == word {
							tokens = append(tokens, rawToken{lineNum, start, len(word), stType})
							break
						}
						if ed, ok := stmt.(*ast.EnumDef); ok && ed.Name == word {
							tokens = append(tokens, rawToken{lineNum, start, len(word), stType})
							break
						}
					}
				}

				continue
			}

			i++
		}
	}

	// Sort by position
	sort.Slice(tokens, func(i, j int) bool {
		if tokens[i].line != tokens[j].line {
			return tokens[i].line < tokens[j].line
		}
		return tokens[i].col < tokens[j].col
	})

	// Encode as delta format
	var data []uint32
	prevLine, prevCol := 0, 0
	for _, t := range tokens {
		deltaLine := t.line - prevLine
		deltaCol := t.col
		if deltaLine == 0 {
			deltaCol = t.col - prevCol
		}
		data = append(data, uint32(deltaLine), uint32(deltaCol), uint32(t.length), t.tokenType, 0)
		prevLine = t.line
		prevCol = t.col
	}
	return data
}

func isIdentStart(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_'
}

func isIdentChar(ch byte) bool {
	return isIdentStart(ch) || (ch >= '0' && ch <= '9')
}

func prevNonSpace(line string, pos int) byte {
	for i := pos - 1; i >= 0; i-- {
		if line[i] != ' ' && line[i] != '\t' {
			return line[i]
		}
	}
	return 0
}
