if exists('b:current_syntax') | finish | endif
syntax case match
syntax keyword borkKeyword fn pred type sealed match if else return unsafe where and or trust rule generate yield for break continue
syntax keyword borkContextual import use class instance instances test private derive uses needs nothing ambient logged propagated lazy async scope with in resource go comptime mock select metadata
syntax keyword borkBoolean true false
syntax match borkType '\<[A-Z][A-Za-z0-9_]*\>'
syntax match borkNumber '\<\d[0-9A-Fa-f_xXbBoO]*\%(\.\d[0-9_]*\)\?\%([eE][+-]\=[0-9_]*\)\?\>'
syntax match borkTagGroup '\%([[:alnum:]_})]\s\+\)\@<=[a-z][A-Za-z0-9_]*\ze[ \t]*{'
syntax match borkTupleIndex '\.\zs[0-9]\+'
syntax match borkOperator '|>\|=>\|[+*/%!?=<>|&^-]'
syntax region borkString start=+"+ skip=+\\.+ end=+"+ contains=borkEscape
syntax match borkEscape '\\.' contained
syntax region borkRune start=+'+ skip=+\\.+ end=+'+ contains=borkEscape
syntax region borkInterpolationNested start='{' end='}' contained contains=ALLBUT,borkGo,borkGoNested,borkTypedString
syntax region borkInterpolation matchgroup=borkInterpolationDelimiter start='${' end='}' contained contains=ALLBUT,borkGo,borkGoNested,borkTypedString
syntax match borkInterpolationName '\$[A-Za-z][A-Za-z0-9_]*' contained
syntax match borkDollar '\$\$' contained
syntax region borkTypedString start=+\<[A-Za-z][A-Za-z0-9_]*\%(\.[A-Za-z][A-Za-z0-9_]*\)\?"+ skip=+\\.+ end=+"+ contains=borkEscape,borkInterpolation,borkInterpolationName,borkDollar
syntax region borkComment start='/\*' end='\*/' contains=borkTodo
syntax match borkComment '//.*$' contains=borkTodo
syntax match borkShebang '\%^#!.*$'
syntax keyword borkTodo TODO FIXME XXX contained
syntax include @borkGo syntax/go.vim
unlet! b:current_syntax
syntax region borkGoNested start='{' end='}' contained contains=@borkGo,borkGoNested
syntax region borkGo matchgroup=borkKeyword start='\<unsafe\s\+go\s*{' end='}' contains=@borkGo,borkGoNested
highlight default link borkKeyword Keyword
highlight default link borkContextual Keyword
highlight default link borkBoolean Boolean
highlight default link borkType Type
highlight default link borkTagGroup Identifier
highlight default link borkNumber Number
highlight default link borkTupleIndex Identifier
highlight default link borkOperator Operator
highlight default link borkString String
highlight default link borkTypedString String
highlight default link borkRune Character
highlight default link borkEscape SpecialChar
highlight default link borkInterpolationName Identifier
highlight default link borkInterpolationDelimiter Special
highlight default link borkDollar String
highlight default link borkComment Comment
highlight default link borkShebang Comment
highlight default link borkTodo Todo
let b:current_syntax = 'bork'

syntax match borkContextual /\<is\>\ze\s\+[^[:space:](:=]/
