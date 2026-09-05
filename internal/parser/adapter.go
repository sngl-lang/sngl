package parser

// tokenAt returns the original Token for terminal leaf index n in the egg []int32 parse tree.
// n is the token-consumption index assigned by the egg RecScanner (0-based, whitespace skipped).
func tokenAt(filtered []Token, n int32) Token {
	if int(n) < 0 || int(n) >= len(filtered) {
		// One past the end is an ordinary position in the tree -- an i18n
		// template's closing segment lands there. EOF rather than ILLEGAL:
		// ILLEGAL now means a lex error was recorded for it, and no error was.
		return Token{Type: EOF}
	}
	return filtered[n]
}
