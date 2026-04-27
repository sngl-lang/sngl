The html codegen has scope quirks — file-level consts don't propagate as component props, and for-loops only unroll at the const's declaring scope, so the search index lives at website.sngl top-level rather than inside docui.

revisit highlighted code in the tutorial once cross-package const folding works
