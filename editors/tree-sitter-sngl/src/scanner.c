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
  uint8_t interp_depth;
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
  buffer[1] = s->interp_depth;
  return 2;
}

void tree_sitter_sngl_external_scanner_deserialize(void *payload,
                                                    const char *buffer,
                                                    unsigned length) {
  Scanner *s = (Scanner *)payload;
  s->in_string = length > 0 && buffer[0] != 0;
  s->interp_depth = length > 1 ? (uint8_t)buffer[1] : 0;
}

bool tree_sitter_sngl_external_scanner_scan(void *payload, TSLexer *lexer,
                                             const bool *valid_symbols) {
  Scanner *s = (Scanner *)payload;

  // --- String interpolation end: } closing interpolation ---
  // Check this first, before ASI can claim the }.
  if (valid_symbols[STRING_INTERPOLATION_END] && s->interp_depth > 0 &&
      lexer->lookahead == '}') {
    lexer->advance(lexer, false);
    lexer->mark_end(lexer);
    lexer->result_symbol = STRING_INTERPOLATION_END;
    s->interp_depth--;
    s->in_string = true;
    return true;
  }

  // --- String content ---
  // When STRING_CONTENT is valid, we're inside a string_literal.
  if (valid_symbols[STRING_CONTENT]) {
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

    // No content — check if this is a { for interpolation start.
    if (lexer->lookahead == '{' && valid_symbols[STRING_INTERPOLATION_START]) {
      lexer->advance(lexer, false);
      lexer->mark_end(lexer);
      lexer->result_symbol = STRING_INTERPOLATION_START;
      s->in_string = false;
      s->interp_depth++;
      return true;
    }

    // Must be closing " — let grammar handle it.
    return false;
  }

  // --- String interpolation start (fallback, not inside STRING_CONTENT) ---
  if (valid_symbols[STRING_INTERPOLATION_START] && s->in_string &&
      lexer->lookahead == '{') {
    lexer->advance(lexer, false);
    lexer->mark_end(lexer);
    lexer->result_symbol = STRING_INTERPOLATION_START;
    s->in_string = false;
    s->interp_depth++;
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
    // Only insert ASI before } when NOT inside string interpolation.
    if (lexer->lookahead == '}' && s->interp_depth == 0) return true;
  }

  return false;
}
