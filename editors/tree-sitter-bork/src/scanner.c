#include "tree_sitter/parser.h"
#include <stdlib.h>
#include <string.h>

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

// After a leading dot, => or in distinguishes a pattern from a selector chain.
// mark_end remains before this lookahead so strings/comments stay in the tree.
static bool context_pattern(TSLexer *lexer) {
  while ((lexer->lookahead >= 'a' && lexer->lookahead <= 'z') ||
         (lexer->lookahead >= 'A' && lexer->lookahead <= 'Z') ||
         (lexer->lookahead >= '0' && lexer->lookahead <= '9') ||
         lexer->lookahead == '_' || lexer->lookahead >= 0x80) lexer->advance(lexer, false);
  skip_space_comments(lexer);
  if (lexer->lookahead == '{' || lexer->lookahead == '(') {
    int open = lexer->lookahead;
    int close = open == '{' ? '}' : ')';
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
        if (c == open) depth++;
        if (c == close) depth--;
        if (!lexer->eof(lexer)) lexer->advance(lexer, false);
      }
    }
    skip_space_comments(lexer);
  }
  if (lexer->lookahead == 'i') {
    lexer->advance(lexer, false);
    if (lexer->lookahead != 'n') return false;
    lexer->advance(lexer, false);
    int c = lexer->lookahead;
    return !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
             (c >= '0' && c <= '9') || c == '_' || c >= 0x80);
  }
  if (lexer->lookahead != '=') return false;
  lexer->advance(lexer, false);
  return lexer->lookahead == '>';
}

static bool line_continuation(TSLexer *lexer) {
  if (lexer->lookahead == '|') {
    lexer->advance(lexer, false);
    return lexer->lookahead == '>';
  }
  if (lexer->lookahead == '.') {
    lexer->advance(lexer, false);
    int c = lexer->lookahead;
    return ((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c >= 0x80) && !context_pattern(lexer);
  }
  return false;
}

typedef struct { bool comment_newline; bool control_newline; } Scanner;

void *tree_sitter_bork_external_scanner_create(void) { return calloc(1, sizeof(Scanner)); }
void tree_sitter_bork_external_scanner_destroy(void *payload) { free(payload); }
unsigned tree_sitter_bork_external_scanner_serialize(void *payload, char *buffer) {
  buffer[0] = ((Scanner *)payload)->comment_newline;
  buffer[1] = ((Scanner *)payload)->control_newline;
  return 2;
}
void tree_sitter_bork_external_scanner_deserialize(void *payload, const char *buffer, unsigned length) {
  ((Scanner *)payload)->comment_newline = length > 0 && buffer[0];
  ((Scanner *)payload)->control_newline = length > 1 && buffer[1];
}

// Go is opaque to the bork parser. Count braces outside Go literals/comments;
// the Go injection parses the resulting content separately.
bool tree_sitter_bork_external_scanner_scan(void *payload, TSLexer *lexer, const bool *valid) {
  Scanner *scanner = payload;
  if (valid[2]) return false;
  if (!valid[0]) {
    while (lexer->lookahead == ' ' || lexer->lookahead == '\t' || lexer->lookahead == '\r') lexer->advance(lexer, true);
  }
  if (valid[1]) {
    bool newline = scanner->comment_newline;
    scanner->comment_newline = false;
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
      bool continuation = line_continuation(lexer);
      if (!continuation || scanner->control_newline) {
        scanner->control_newline = false;
        lexer->result_symbol = 1;
        return true;
      }
      return false;
    }
  }
  if (!valid[0] && valid[7] && (valid[1] || valid[6] || scanner->control_newline) && lexer->lookahead == '/') {
    lexer->advance(lexer, false);
    if (lexer->lookahead == '/') {
      while (!lexer->eof(lexer) && lexer->lookahead != '\n') lexer->advance(lexer, false);
    } else if (lexer->lookahead == '*') {
      lexer->advance(lexer, false);
      int previous = 0;
      bool newline = false;
      while (!lexer->eof(lexer)) {
        int c = lexer->lookahead;
        if (c == '\n') newline = true;
        lexer->advance(lexer, false);
        if (previous == '*' && c == '/') break;
        previous = c;
      }
      // Preserve the comment node, then emit a separator when it crosses
      // a line at a position where the grammar can end an item.
      scanner->comment_newline = newline && (valid[1] || valid[6] || scanner->control_newline);
    } else return false;
    lexer->mark_end(lexer);
    if (scanner->comment_newline && !scanner->control_newline) {
      // Decide before returning the comment: a failed newline scan cannot
      // persist cleared state, which would split a continued call later.
      skip_space_comments(lexer);
      if (line_continuation(lexer)) scanner->comment_newline = false;
    }
    lexer->result_symbol = 7;
    return true;
  }
  if (valid[6]) {
    // Only a same-line name followed by '{' starts a tag group. Look ahead
    // without consuming the brace, so where/derive and new variants keep
    // their ordinary tokens. Newlines are handled above as separators.
    while (lexer->lookahead == ' ' || lexer->lookahead == '\t' || lexer->lookahead == '\r') lexer->advance(lexer, true);
    int first = lexer->lookahead;
    if (!((first >= 'a' && first <= 'z') || (first >= 'A' && first <= 'Z') || first >= 0x80)) return false;
    char name[16];
    unsigned length = 0;
    do {
      if (length < sizeof(name) - 1) name[length] = lexer->lookahead < 0x80 ? (char)lexer->lookahead : '?';
      length++;
      lexer->advance(lexer, false);
    } while ((lexer->lookahead >= 'a' && lexer->lookahead <= 'z') ||
             (lexer->lookahead >= 'A' && lexer->lookahead <= 'Z') ||
             (lexer->lookahead >= '0' && lexer->lookahead <= '9') ||
             lexer->lookahead == '_' || lexer->lookahead >= 0x80);
    if (length < sizeof(name)) {
      name[length] = 0;
      const char *keywords[] = {"fn", "pred", "type", "sealed", "where", "and", "or", "trust", "rule", "return", "if", "else", "match", "generate", "yield", "for", "break", "continue", "true", "false"};
      for (unsigned i = 0; i < sizeof(keywords) / sizeof(keywords[0]); i++) {
        if (strcmp(name, keywords[i]) == 0) return false;
      }
    }
    lexer->mark_end(lexer);
    while (lexer->lookahead == ' ' || lexer->lookahead == '\t' || lexer->lookahead == '\r') lexer->advance(lexer, false);
    // Inline block comments may separate the qualifier from its brace.
    while (lexer->lookahead == '/') {
      lexer->advance(lexer, false);
      if (lexer->lookahead != '*') return false;
      lexer->advance(lexer, false);
      int previous = 0;
      while (!lexer->eof(lexer)) {
        int c = lexer->lookahead;
        if (c == '\n') return false;
        lexer->advance(lexer, false);
        if (previous == '*' && c == '/') break;
        previous = c;
      }
      while (lexer->lookahead == ' ' || lexer->lookahead == '\t' || lexer->lookahead == '\r') lexer->advance(lexer, false);
    }
    if (lexer->lookahead != '{') return false;
    lexer->result_symbol = 6;
    return true;
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
    // Bare control keywords end before a following selector continuation.
    scanner->control_newline = true;
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
