;;; bork-mode.el --- bork editing and language server support -*- lexical-binding: t; -*-
;; Package-Requires: ((emacs "29.1"))
;; Version: 0.1.0
;; URL: https://github.com/GiGurra/bork
;; SPDX-License-Identifier: MIT

;;; Commentary:
;; Highlighting is local; diagnostics and formatting come from bork lsp.
;; Enable Eglot with M-x eglot.  bork-ts-mode uses the shared bork grammar.

;;; Code:
(require 'cl-lib)
(require 'project)
(require 'treesit)

(defgroup bork nil "Editing bork programs." :group 'languages)
(defcustom bork-indent-offset 2 "Spaces used for indentation." :type 'integer :group 'bork)
(defconst bork-keywords
  '("fn" "pred" "type" "sealed" "match" "if" "else" "return" "unsafe" "where"
    "and" "or" "trust" "rule" "generate" "yield" "for" "break" "continue"
    "import" "use" "class" "instance" "instances" "providers" "test" "private"
    "derive" "uses" "needs" "nothing" "ambient" "logged" "propagated" "lazy"
    "async" "scope" "with" "in" "resource" "go" "comptime" "mock"))
(defvar bork-mode-syntax-table
  (let ((table (make-syntax-table)))
    (modify-syntax-entry ?/ ". 124b" table)
    (modify-syntax-entry ?* ". 23" table)
    (modify-syntax-entry ?\n "> b" table)
    (modify-syntax-entry ?' "\"" table)
    (modify-syntax-entry ?_ "w" table)
    table))
(defconst bork-font-lock-keywords
  `(("\\_<\\(is\\)\\_>[[:space:]]+[^[:space:](:=]" 1 font-lock-keyword-face)
    (,(regexp-opt bork-keywords 'symbols) . font-lock-keyword-face)
    (,(regexp-opt '("true" "false") 'symbols) . font-lock-constant-face)
    ("\\_<[A-Z][[:alnum:]_]*\\_>" . font-lock-type-face)
    ("\\_<\\(?:fn\\|pred\\|rule\\) +\\([[:alpha:]][[:alnum:]_]*\\)" 1 font-lock-function-name-face)))

(defun bork-indent-line ()
  "Indent using delimiter structure; use LSP formatting for canonical layout."
  (interactive)
  (let* ((position (- (point-max) (point)))
         (opening (nth 1 (syntax-ppss (line-beginning-position))))
         (indent (if opening
                     (save-excursion (goto-char opening)
                                     (+ (current-indentation) bork-indent-offset))
                   0)))
    (when (save-excursion (back-to-indentation) (looking-at-p "[]})]"))
      (setq indent (max 0 (- indent bork-indent-offset))))
    (indent-line-to indent)
    (when (> (- (point-max) position) (point))
      (goto-char (- (point-max) position)))))

(defun bork--project-find (directory)
  (when-let* ((root (locate-dominating-file directory "bork.mod")))
    (cons 'bork root)))
(cl-defmethod project-root ((project (head bork))) (cdr project))

(defun bork--setup ()
  (setq-local comment-start "// " comment-end "" indent-tabs-mode nil
              tab-width bork-indent-offset
              imenu-generic-expression
              '(("Functions" "^[[:space:]]*fn +\\([[:alpha:]][[:alnum:]_]*\\)" 1)
                ("Types" "^[[:space:]]*type +\\([[:alpha:]][[:alnum:]_]*\\)" 1)
                ("Predicates" "^[[:space:]]*pred +\\([[:alpha:]][[:alnum:]_]*\\)" 1)))
  (add-hook 'project-find-functions #'bork--project-find nil t))

;;;###autoload
(define-derived-mode bork-mode prog-mode "bork"
  "Edit bork source with lexical highlighting."
  :syntax-table bork-mode-syntax-table
  (bork--setup)
  (setq-local font-lock-defaults '(bork-font-lock-keywords)
              indent-line-function #'bork-indent-line))

;;;###autoload
(define-derived-mode bork-ts-mode prog-mode "bork[ts]"
  "Edit bork source using the shared tree-sitter grammar."
  :syntax-table bork-mode-syntax-table
  (unless (treesit-ready-p 'bork t)
    (user-error "Install the bork tree-sitter parser before enabling bork-ts-mode"))
  (bork--setup)
  (treesit-parser-create 'bork)
  (setq-local
   treesit-font-lock-settings
   (treesit-font-lock-rules
    :language 'bork :feature 'comment '([(comment) (shebang)] @font-lock-comment-face)
    :language 'bork :feature 'keyword
    `([,@(remove "nothing" bork-keywords)
       (bare_return) (bare_break) (bare_continue)] @font-lock-keyword-face
      (is_expression "is" @font-lock-keyword-face))
    :language 'bork :feature 'string '([(string) (interpolated_string) (rune)] @font-lock-string-face)
    :language 'bork :feature 'type '( (type_declaration name: (identifier) @font-lock-type-face)
                                   (context_pattern name: (identifier) @font-lock-type-face)
                                   (type (qualified_name (identifier) @font-lock-type-face)))
    :language 'bork :feature 'definition
    '([(function_declaration name: (identifier) @font-lock-function-name-face)
       (predicate_declaration name: (identifier) @font-lock-function-name-face)])
    :language 'bork :feature 'number '([(number) @font-lock-number-face
                                (tuple_index) @font-lock-variable-name-face])
    :language 'bork :feature 'constant '(["true" "false"] @font-lock-constant-face))
   treesit-font-lock-feature-list '((comment keyword) (string type definition) (number constant))
   treesit-simple-indent-rules
   `((bork ((node-is "}") parent-bol 0)
           ((node-is "]") parent-bol 0)
           ((node-is ")") parent-bol 0)
           ((parent-is "block") parent-bol ,bork-indent-offset)
           ((parent-is "record_type") parent-bol ,bork-indent-offset)
           ((parent-is "record_literal") parent-bol ,bork-indent-offset)
           ((parent-is "tuple_literal") parent-bol ,bork-indent-offset)
           ((parent-is "tuple_pattern") parent-bol ,bork-indent-offset)
           ((parent-is "tuple_type") parent-bol ,bork-indent-offset)
           ((parent-is "match_expression") parent-bol ,bork-indent-offset)
           (no-node parent-bol 0))))
  (treesit-major-mode-setup))

(with-eval-after-load 'eglot
  (add-to-list 'eglot-server-programs '(((bork-mode :language-id "bork") (bork-ts-mode :language-id "bork")) "bork" "lsp")))
;;;###autoload
(add-to-list 'auto-mode-alist '("\\.bork\\'" . bork-mode))
;;;###autoload
(add-to-list 'interpreter-mode-alist '("bork" . bork-mode))
;;;###autoload
(add-to-list 'magic-mode-alist '("\\`#!.*\\_<bork\\_>" . bork-mode))
(provide 'bork-mode)
;;; bork-mode.el ends here
