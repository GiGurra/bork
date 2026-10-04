augroup bork_filetype
  autocmd!
  autocmd BufRead,BufNewFile *.bork setfiletype bork
  autocmd BufRead,BufNewFile * if getline(1) =~# '^#!.*\<bork\>' | setfiletype bork | endif
augroup END
