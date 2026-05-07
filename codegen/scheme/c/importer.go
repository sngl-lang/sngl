package c

// predefinedPreamble is the minimal predefined C environment needed by cc/v4.
const predefinedPreamble = `
int __predefined_declarator;
#if defined(__i386__) || defined(__arm__)
typedef unsigned __predefined_size_t;
#else
typedef unsigned long long __predefined_size_t;
#endif
`
