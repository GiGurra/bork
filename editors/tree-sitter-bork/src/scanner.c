#include "tree_sitter/parser.h"
#include <stdlib.h>

static void skip_space_comments(TSLexer *lexer) {
  for (;;) {
    while (lexer->lookahead == ' ' || lexer->lookahead == '\t' || lexer->lookahead == '\r' || lexer->lookahead == '\n') lexer->advance(lexer, false);
    if (lexer->lookahead != '/') return;
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
    } else return;
  }
}

// After a leading dot, => distinguishes a context arm from a selector chain.
// mark_end remains before this lookahead so strings/comments stay in the tree.
static bool context_arm(TSLexer *lexer) {
  while ((lexer->lookahead >= 'a' && lexer->lookahead <= 'z') ||
         (lexer->lookahead >= 'A' && lexer->lookahead <= 'Z') ||
         (lexer->lookahead >= '0' && lexer->lookahead <= '9') ||
         lexer->lookahead == '_' || lexer->lookahead >= 0x80) lexer->advance(lexer, false);
  skip_space_comments(lexer);
  if (lexer->lookahead == '{') {
    unsigned depth = 1;
    lexer->advance(lexer, false);
    while (!lexer->eof(lexer) && depth) {
      skip_space_comments(lexer);
      int c = lexer->lookahead;
      if (c == '"' || c == '\'') {
        int quote = c;
        lexer->advance(lexer, false);
        while (!lexer->eof(lexer) && lexer->lookahead != quote) {
          if (lexer->lookahead == '\\') lexer->advance(lexer, false);
          if (!lexer->eof(lexer)) lexer->advance(lexer, false);
        }
        if (!lexer->eof(lexer)) lexer->advance(lexer, false);
      } else {
        if (c == '{') depth++;
        if (c == '}') depth--;
        if (!lexer->eof(lexer)) lexer->advance(lexer, false);
      }
    }
    skip_space_comments(lexer);
  }
  if (lexer->lookahead != '=') return false;
  lexer->advance(lexer, false);
  return lexer->lookahead == '>';
}

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
        continuation = ((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c >= 0x80) && !context_arm(lexer);
      }
      if (!continuation) { lexer->result_symbol = 1; return true; }
      return false;
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
    bool ends_line = false;
    for (;;) {
      while (lexer->lookahead == ' ' || lexer->lookahead == '\t' || lexer->lookahead == '\r') lexer->advance(lexer, false);
      if (lexer->lookahead == '\n' || lexer->eof(lexer)) { ends_line = true; break; }
      if (lexer->lookahead != '/') break;
      lexer->advance(lexer, false);
      if (lexer->lookahead == '/') { ends_line = true; break; }
      if (lexer->lookahead != '*') break;
      lexer->advance(lexer, false);
      int previous = 0;
      while (!lexer->eof(lexer)) {
        int c = lexer->lookahead;
        if (c == '\n') ends_line = true;
        lexer->advance(lexer, false);
        if (previous == '*' && c == '/') break;
        previous = c;
      }
      if (ends_line) break;
    }
    if (!ends_line) return false;
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
