if exists('b:did_indent') | finish | endif
let b:did_indent = 1
setlocal cindent shiftwidth=2 expandtab
let b:undo_indent = 'setlocal cindent< shiftwidth< expandtab<'
