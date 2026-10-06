set nocompatible
set nomore
let &runtimepath = getcwd() . '/editors/vim,' . &runtimepath
filetype plugin indent on
syntax enable
runtime ftdetect/bork.vim
new
setfiletype bork
call setline(1, ['fn main() {', '  text = s"hello $name ${value}"', '}', 'fn raw() unsafe go {', '  if true { println("}") }', '}', 'fn after() {}'])
syntax sync fromstart
call assert_equal('bork', &filetype)
call assert_equal('borkKeyword', synIDattr(synID(1, 1, 1), 'name'))
call assert_equal('borkTypedString', synIDattr(synID(2, 13, 1), 'name'))
call assert_equal('borkInterpolationName', synIDattr(synID(2, 18, 1), 'name'))
call assert_equal('borkKeyword', synIDattr(synID(7, 1, 1), 'name'))
call assert_equal(2, &shiftwidth)
call assert_equal('// %s', &commentstring)
call append('$', 'text = s"Hi ${f({"x": "}"}) + 1}!"')
let number_column = stridx(getline(8), '+ 1') + 3
call assert_equal('borkNumber', synIDattr(synID(8, number_column, 1), 'name'))
call append('$', 'type Reply[T] = sealed { Found(T, String), Gone }')
call append('$', 'match(x) { Reply[Int].Found(n, _) => n, .Gone => 0 }')
for line in [9, 10]
  let variant_column = stridx(getline(line), 'Found') + 1
  call assert_equal('borkType', synIDattr(synID(line, variant_column, 1), 'name'))
endfor
for file in split(glob('examples/**/*.bork') . "\n" . glob('testdata/cases/**/*.bork'), "\n")
  execute 'edit! ' . fnameescape(file)
  call assert_equal('bork', &filetype, file)
  syntax sync fromstart
  for line in range(1, line('$'))
    call synID(line, max([1, strlen(getline(line))]), 1)
  endfor
endfor
new
call setline(1, ['#!/usr/bin/env -S bork script', 'println("hello")'])
doautocmd BufRead script-without-extension
call assert_equal('bork', &filetype)
if !empty(v:errors)
  call writefile(v:errors, '/dev/stderr')
  cquit
endif
call writefile(['Vim: syntax, embedded Go boundary, options, shebang and all repository sources passed'], '/dev/stdout')
qa!
