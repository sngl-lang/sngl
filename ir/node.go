package ir

// Node is the common interface of every IR statement and expression. Both the
// Stmt and Expr interfaces embed it, so any statement or expression is a Node.
// It exists so the IR visitor (see walkexprs.go) can present a single
// callback — func(Node) — over both kinds, mirroring go/ast's ast.Node.
type Node interface{ irNode() }

// irNode markers. Every concrete Stmt and Expr type implements Node.

// Expressions.
func (*Literal) irNode()     {}
func (*Ident) irNode()       {}
func (*Binary) irNode()      {}
func (*Unary) irNode()       {}
func (*Ternary) irNode()     {}
func (*Call) irNode()        {}
func (*Conversion) irNode()  {}
func (*Select) irNode()      {}
func (*Index) irNode()       {}
func (*StructLit) irNode()   {}
func (*ListLit) irNode()     {}
func (*MapLitIR) irNode()    {}
func (*Spread) irNode()      {}
func (*Lambda) irNode()      {}
func (*Closure) irNode()     {}
func (*ContextRead) irNode() {}

// Statements.
func (*NodeInst) irNode()         {}
func (*CallStmt) irNode()         {}
func (*SlotInst) irNode()         {}
func (*ErrorBoundary) irNode()    {}
func (*Assign) irNode()           {}
func (*Toggle) irNode()           {}
func (*Emit) irNode()             {}
func (*LocalVar) irNode()         {}
func (*Return) irNode()           {}
func (*Break) irNode()            {}
func (*Continue) irNode()         {}
func (*If) irNode()               {}
func (*For) irNode()              {}
func (*ContextProvider) irNode()  {}
func (*CanvasRedrawStmt) irNode() {}
