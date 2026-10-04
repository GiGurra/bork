#include "tree_sitter/parser.h"
#include <stdlib.h>

void *tree_sitter_bork_external_scanner_create(void) { return NULL; }
void tree_sitter_bork_external_scanner_destroy(void *payload) { (void)payload; }
unsigned tree_sitter_bork_external_scanner_serialize(void *payload, char *buffer) {
  (void)payload; (void)buffer; return 0;
}
void tree_sitter_bork_external_scanner_deserialize(void *payload, const char *buffer, unsigned length) {
  (void)payload; (void)buffer; (void)length;
}

// Go is opaque to the bork parser. Count braces outside Go literals/comments;
// the Go injection parses the resulting content separately.
bool tree_sitter_bork_external_scanner_scan(void *payload, TSLexer *lexer, const bool *valid) {
  (void)payload;
  if (valid[2]) return false;
  if (valid[1]) {
    bool newline = false;
    while (lexer->lookahead == ' ' || lexer->lookahead == '\t' || lexer->lookahead == '\r' || lexer->lookahead == '\n') {
      if (lexer->lookahead == '\n') newline = true;
      lexer->advance(lexer, true);
    }
    if (newline) {
      lexer->mark_end(lexer);
      // Look through comments without including them in the newline token.
      // This keeps comments in the tree and joins comment-separated chains.
      while (lexer->lookahead == '/') {
        lexer->advance(lexer, false);
        if (lexer->lookahead == '/') {
          while (!lexer->eof(lexer) && lexer->lookahead != '\n') lexer->advance(lexer, false);
        } else if (lexer->lookahead == '*') {
          lexer->advance(lexer, false);
          int previous = 0;
          while (!lexer->eof(lexer)) {
            int c = lexer->lookahead;
            lexer->advance(lexer, false);
            if (previous == '*' && c == '/') break;
            previous = c;
          }
        } else break;
        while (lexer->lookahead == ' ' || lexer->lookahead == '\t' || lexer->lookahead == '\r' || lexer->lookahead == '\n') lexer->advance(lexer, false);
      }
      bool continuation = false;
      if (lexer->lookahead == '|') {
        lexer->advance(lexer, false); continuation = lexer->lookahead == '>';
      } else if (lexer->lookahead == '.') {
        lexer->advance(lexer, false);
        int c = lexer->lookahead;
        continuation = (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c >= 0x80;
      }
      if (!continuation) { lexer->result_symbol = 1; return true; }
    }
  }
  if (valid[3] || valid[4] || valid[5]) {
    while (lexer->lookahead == ' ' || lexer->lookahead == '\t' || lexer->lookahead == '\r' || lexer->lookahead == '\n') lexer->advance(lexer, true);
  }
  const char *control[] = {"return", "break", "continue"};
  for (unsigned i = 0; i < 3; i++) {
    if (!valid[i + 3] || lexer->lookahead != control[i][0]) continue;
    const char *word = control[i];
    while (*word && lexer->lookahead == *word) { lexer->advance(lexer, false); word++; }
    if (*word) return false;
    lexer->mark_end(lexer);
    while (lexer->lookahead == ' ' || lexer->lookahead == '\t' || lexer->lookahead == '\r') lexer->advance(lexer, false);
    if (lexer->lookahead == '/') {
      lexer->advance(lexer, false);
      if (lexer->lookahead != '/') return false;
      while (!lexer->eof(lexer) && lexer->lookahead != '\n') lexer->advance(lexer, false);
    }
    if (lexer->lookahead != '\n' && !lexer->eof(lexer)) return false;
    lexer->result_symbol = i + 3;
    return true;
  }
  if (!valid[0] || lexer->lookahead == '}' || lexer->eof(lexer)) return false;
  unsigned depth = 0;
  int quote = 0;
  bool line_comment = false, block_comment = false, escaped = false;
  int previous = 0;
  while (!lexer->eof(lexer)) {
    int c = lexer->lookahead;
    if (!quote && !line_comment && !block_comment && c == '}' && depth == 0) break;
    lexer->advance(lexer, false);
    if (line_comment) { if (c == '\n') line_comment = false; }
    else if (block_comment) { if (previous == '*' && c == '/') { block_comment = false; c = 0; } }
    else if (quote) {
      if (escaped) escaped = false;
      else if (c == '\\' && quote != '`') escaped = true;
      else if (c == quote) quote = 0;
    } else if (previous == '/' && c == '/') line_comment = true;
    else if (previous == '/' && c == '*') block_comment = true;
    else if (c == '"' || c == '\'' || c == '`') quote = c;
    else if (c == '{') depth++;
    else if (c == '}') depth--;
    previous = c;
  }
  lexer->mark_end(lexer);
  lexer->result_symbol = 0;
  return true;
}
