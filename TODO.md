Design a special purpose language for SNGL. Generates the same AST, but allows syntatic imposibilities compared to KDL. Include expressions that can be represented as a CEL AST.

- Create a length type like CSS's units

```
unit duration(s = 1000, ms = 1, m = s * 60, h = m * 60)
unit length()
```
