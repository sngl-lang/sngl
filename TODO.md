Design a special purpose language for SNGL. Generates the same AST, but allows syntatic imposibilities compared to KDL. Include expressions that can be represented as a CEL AST.

- Create a length type like CSS's units

Duration is currently a special case in parsing. Let's make units their own concept in SNGL. A unit is defined with a set of suffixes and their factors as constant expressions. Other suffixes may be used as a constant value in the expressions.

```
unit duration(s = 1000, ms = 1, m = s * 60, h = m * 60)
```

The parser will take an numeric value with an alphabetic suffix as a unit literal. A ternary with constant values can be used as a constant expression so you can define per-platform unit conversions for units associated with size.

A unit may have multiple independent base suffixes. In generated code, these are tracked separately. That allows units that don't convert/combine. For example: time spans need to track months, days, and seconds separate, since months and days can differ in length. For CSS units, em and px are different bases.

In expressions expecting a unit type value, the suffixes may be used as a constant for a multiplier. Unit literals may only be used in a context where a unit type is expected. All types can be used like a function to cast/convert, so `var x = duration(5ms)` is acceptable. You can also add various units together `var x duration = 1h + 30m`

Update styles to use a measurement unit for many of the dyn values. Include common CSS units.

Unit declarations shouldn't have multiple (...) groups. The independent suffixes will be determined by the absence of a relational constant expression.

eg `unit measurement(in = 96px, px, pct)` Inches and pixels are related, but pct is independent.

Convert
