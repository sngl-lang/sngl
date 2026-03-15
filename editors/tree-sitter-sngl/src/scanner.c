#include "tree_sitter/parser.h"

#include <stdbool.h>
#include <stdlib.h>
#include <string.h>

enum TokenType {
  AUTOMATIC_SEMICOLON,
  STRING_CONTENT,
  STRING_INTERPOLATION_START,
  STRING_INTERPOLATION_END,
};

typedef struct {
  bool in_string;
} Scanner;

void *tree_sitter_sngl_external_scanner_create(void) {
  return calloc(1, sizeof(Scanner));
}

void tree_sitter_sngl_external_scanner_destroy(void *payload) {
  free(payload);
}

unsigned tree_sitter_sngl_external_scanner_serialize(void *payload,
                                                      char *buffer) {
  Scanner *s = (Scanner *)payload;
  buffer[0] = s->in_string ? 1 : 0;
  return 1;
}

void tree_sitter_sngl_external_scanner_deserialize(void *payload,
                                                    const char *buffer,
                                                    unsigned length) {
  Scanner *s = (Scanner *)payload;
  s->in_string = length > 0 && buffer[0] != 0;
}

bool tree_sitter_sngl_external_scanner_scan(void *payload, TSLexer *lexer,
                                             const bool *valid_symbols) {
  Scanner *s = (Scanner *)payload;

  // --- String content ---
  // Only when we are NOT also expecting AUTOMATIC_SEMICOLON (which means
  // the parser is genuinely inside a string_literal after the opening quote).
  if (valid_symbols[STRING_CONTENT] && !valid_symbols[AUTOMATIC_SEMICOLON]) {
    s->in_string = true;
    bool has_content = false;
    lexer->result_symbol = STRING_CONTENT;
    while (lexer->lookahead != 0) {
      if (lexer->lookahead == '"') break;
      if (lexer->lookahead == '{') break;
      if (lexer->lookahead == '\\') {
        has_content = true;
        lexer->advance(lexer, false);
        if (lexer->lookahead != 0) lexer->advance(lexer, false);
        continue;
      }
      has_content = true;
      lexer->advance(lexer, false);
    }
    if (has_content) {
      lexer->mark_end(lexer);
      return true;
    }
    // No content before " or { — let grammar handle it
    return false;
  }

  // --- String interpolation start: { inside string ---
  if (valid_symbols[STRING_INTERPOLATION_START] && s->in_string &&
      lexer->lookahead == '{') {
    lexer->advance(lexer, false);
    lexer->mark_end(lexer);
    lexer->result_symbol = STRING_INTERPOLATION_START;
    s->in_string = false;
    return true;
  }

  // --- String interpolation end: } closing interpolation ---
  if (valid_symbols[STRING_INTERPOLATION_END] && !s->in_string &&
      lexer->lookahead == '}') {
    lexer->advance(lexer, false);
    lexer->mark_end(lexer);
    lexer->result_symbol = STRING_INTERPOLATION_END;
    s->in_string = true;
    return true;
  }

  // --- Automatic semicolon insertion ---
  if (valid_symbols[AUTOMATIC_SEMICOLON]) {
    lexer->result_symbol = AUTOMATIC_SEMICOLON;
    lexer->mark_end(lexer);

    // EOF → insert semicolon
    if (lexer->eof(lexer)) return true;

    // Scan for newline (skip spaces/tabs only)
    bool found_newline = false;
    while (lexer->lookahead == ' ' || lexer->lookahead == '\t' ||
           lexer->lookahead == '\r' || lexer->lookahead == '\n') {
      if (lexer->lookahead == '\n') found_newline = true;
      lexer->advance(lexer, true);  // skip
    }

    if (found_newline) return true;
    if (lexer->eof(lexer)) return true;
    if (lexer->lookahead == '}') return true;
  }

  return false;
}
