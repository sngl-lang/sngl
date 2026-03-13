package parser

import (
	"fmt"
	"io"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"github.com/google/cel-go/cel"

	kdl "github.com/calico32/kdl-go"
)

// Parse reads a KDL document and produces a typed SNGL AST.
func Parse(filename string, r io.Reader) (*ast.Document, error) {
	kdlDoc, err := kdl.Parse(r)
	if err != nil {
		return nil, fmt.Errorf("KDL parse error: %w", err)
	}

	celEnv, err := cel.NewEnv()
	if err != nil {
		return nil, fmt.Errorf("CEL env error: %w", err)
	}

	p := &parser{filename: filename, celEnv: celEnv}
	doc := p.parseDocument(kdlDoc)

	if len(p.errs) > 0 {
		return doc, p.joinErrors()
	}
	return doc, nil
}

type parser struct {
	filename string
	celEnv   *cel.Env
	errs     []error
}

func (p *parser) pos(node *kdl.Node) ast.Pos {
	loc := node.Location()
	return ast.Pos{Line: loc.Line, Column: loc.Column}
}

func (p *parser) errorf(format string, args ...any) {
	p.errs = append(p.errs, fmt.Errorf(format, args...))
}

func (p *parser) errorAt(pos ast.Pos, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if pos.IsValid() {
		p.errs = append(p.errs, fmt.Errorf("%s: %s", pos, msg))
	} else {
		p.errs = append(p.errs, fmt.Errorf("%s", msg))
	}
}

func (p *parser) joinErrors() error {
	if len(p.errs) == 0 {
		return nil
	}
	msgs := make([]string, len(p.errs))
	for i, e := range p.errs {
		msgs[i] = e.Error()
	}
	return fmt.Errorf("%s", strings.Join(msgs, "\n"))
}

func (p *parser) parseDocument(kdlDoc *kdl.Document) *ast.Document {
	doc := &ast.Document{}

	for _, node := range kdlDoc.Nodes {
		switch node.Name() {
		case "output":
			if o := p.parseOutput(node); o != nil {
				doc.Outputs = append(doc.Outputs, o)
			}
		case "struct":
			if sd := p.parseStructNode(node); sd != nil {
				doc.Structs = append(doc.Structs, sd)
			}
		case "enum":
			if e := p.parseEnumNode(node); e != nil {
				doc.Enums = append(doc.Enums, e)
			}
		case "import":
			if imp := p.parseImport(node); imp != nil {
				doc.Imports = append(doc.Imports, imp)
			}
		case "bind":
			doc.Binds = append(doc.Binds, p.parseBindNode(node)...)
		case "computed":
			doc.Computeds = append(doc.Computeds, p.parseComputedNode(node)...)
		case "component":
			if comp := p.parseComponent(node); comp != nil {
				doc.Components = append(doc.Components, comp)
			}
		case "style":
			if s := p.parseStyleDecl(node); s != nil {
				doc.Styles = append(doc.Styles, s)
			}
		case "styles":
			doc.StyleDefs = append(doc.StyleDefs, p.parseStylesNode(node)...)
		case "app":
			if doc.App != nil {
				p.errorAt(p.pos(node), "duplicate app node")
				continue
			}
			doc.App = p.parseApp(node)
		default:
			p.errorAt(p.pos(node), "unknown top-level node: %q", node.Name())
		}
	}

	return doc
}

func (p *parser) parseOutput(node *kdl.Node) *ast.Output {
	o := &ast.Output{Pos: p.pos(node), Options: make(map[string]string)}

	collectProps := func(n *kdl.Node) {
		for _, key := range n.PropertyOrder() {
			o.Options[key] = n.Properties()[key].String()
		}
	}

	args := node.Arguments()
	children := node.Children()
	collectProps(node)

	// Short form: output go bubbletea [key=value...]
	if len(args) >= 2 {
		o.Lang = args[0].String()
		o.Platform = args[1].String()
		return o
	}

	// Children form: output { <lang> ... }
	if len(args) == 0 && children != nil && len(children.Nodes) == 1 {
		langNode := children.Nodes[0]
		o.Lang = langNode.Name()
		collectProps(langNode)

		langArgs := langNode.Arguments()
		langChildren := langNode.Children()

		// output { go bubbletea [key=value...] [{...}] }
		if len(langArgs) >= 1 {
			o.Platform = langArgs[0].String()
			if langChildren != nil {
				for _, optNode := range langChildren.Nodes {
					optArgs := optNode.Arguments()
					if len(optArgs) >= 1 {
						o.Options[optNode.Name()] = optArgs[0].String()
					}
				}
			}
			return o
		}

		// output { go { bubbletea [key=value...] [{...}] } }
		if langChildren != nil && len(langChildren.Nodes) == 1 {
			platNode := langChildren.Nodes[0]
			o.Platform = platNode.Name()
			collectProps(platNode)
			platChildren := platNode.Children()
			if platChildren != nil {
				for _, optNode := range platChildren.Nodes {
					optArgs := optNode.Arguments()
					if len(optArgs) >= 1 {
						o.Options[optNode.Name()] = optArgs[0].String()
					}
				}
			}
			return o
		}
	}

	p.errorf("output: expected 'output <lang> <platform>' or nested form")
	return nil
}

func (p *parser) parseStructNode(node *kdl.Node) *ast.StructDef {
	args := node.Arguments()
	if len(args) < 1 {
		p.errorf("struct: missing name argument")
		return nil
	}
	sd := &ast.StructDef{Pos: p.pos(node), Name: args[0].String()}
	children := node.Children()
	if children == nil {
		p.errorf("struct %q: expected field definitions", sd.Name)
		return sd
	}
	for _, child := range children.Nodes {
		childArgs := child.Arguments()
		if len(childArgs) < 1 {
			p.errorf("struct %q: field %q missing type and default", sd.Name, child.Name())
			continue
		}
		expr := p.toExpr(childArgs[0])
		sd.Fields = append(sd.Fields, &ast.StructField{
			Pos:     p.pos(child),
			Name:    child.Name(),
			Type:    expr.TypeHint,
			Default: expr,
		})
	}
	return sd
}

func (p *parser) parseImport(node *kdl.Node) *ast.Import {
	args := node.Arguments()
	if len(args) < 1 {
		p.errorf("import: missing path argument")
		return nil
	}
	if args[0].Kind() != kdl.String {
		p.errorf("import: path must be a string")
		return nil
	}
	return &ast.Import{Pos: p.pos(node), Path: args[0].String()}
}

func (p *parser) parseEnumNode(node *kdl.Node) *ast.EnumDef {
	args := node.Arguments()
	if len(args) < 1 {
		p.errorf("enum: missing name argument")
		return nil
	}
	e := &ast.EnumDef{Pos: p.pos(node), Name: args[0].String()}
	children := node.Children()
	if children == nil {
		p.errorf("enum %q: expected value definitions", e.Name)
		return e
	}
	for _, child := range children.Nodes {
		e.Values = append(e.Values, child.Name())
	}
	return e
}

func (p *parser) parseBindNode(node *kdl.Node) []*ast.Bind {
	args := node.Arguments()
	// Individual form: bind "name" (type)value
	if len(args) >= 2 {
		name := args[0].String()
		expr := p.toExpr(args[1])
		return []*ast.Bind{{Pos: p.pos(node), Name: name, Init: expr}}
	}
	// Block form: bind { name (type)value; ... }
	children := node.Children()
	if children == nil {
		p.errorf("bind: expected arguments or children")
		return nil
	}
	var binds []*ast.Bind
	for _, child := range children.Nodes {
		childArgs := child.Arguments()
		if len(childArgs) < 1 {
			p.errorf("bind block: child %q missing value", child.Name())
			continue
		}
		binds = append(binds, &ast.Bind{
			Pos:  p.pos(child),
			Name: child.Name(),
			Init: p.toExpr(childArgs[0]),
		})
	}
	return binds
}

func (p *parser) parseComputedNode(node *kdl.Node) []*ast.Computed {
	args := node.Arguments()
	// Individual form: computed "name" (cel)"expr"
	if len(args) >= 2 {
		name := args[0].String()
		expr := p.toExpr(args[1])
		return []*ast.Computed{{Pos: p.pos(node), Name: name, Expr: expr}}
	}
	// Block form
	children := node.Children()
	if children == nil {
		p.errorf("computed: expected arguments or children")
		return nil
	}
	var computeds []*ast.Computed
	for _, child := range children.Nodes {
		childArgs := child.Arguments()
		if len(childArgs) < 1 {
			p.errorf("computed block: child %q missing expression", child.Name())
			continue
		}
		computeds = append(computeds, &ast.Computed{
			Pos:  p.pos(child),
			Name: child.Name(),
			Expr: p.toExpr(childArgs[0]),
		})
	}
	return computeds
}

func (p *parser) parseComponent(node *kdl.Node) *ast.Component {
	args := node.Arguments()
	if len(args) < 1 {
		p.errorf("component: missing name argument")
		return nil
	}
	comp := &ast.Component{Pos: p.pos(node), Name: args[0].String()}

	// Property form: component "Counter" label=(string)"" start=(int)0
	for _, key := range node.PropertyOrder() {
		val := node.Properties()[key]
		comp.Params = append(comp.Params, &ast.Param{
			Pos:     p.pos(node),
			Name:    key,
			Default: p.toExpr(val),
		})
	}

	children := node.Children()
	if children == nil {
		return comp
	}

	for _, child := range children.Nodes {
		switch child.Name() {
		case "@param":
			comp.Params = append(comp.Params, p.parseParam(child))
		case "@prop":
			comp.PropDecls = append(comp.PropDecls, p.parsePropDecl(child))
		case "@event":
			comp.EventDecls = append(comp.EventDecls, p.parseEventDecl(child))
		case "@children":
			if args := child.Arguments(); len(args) >= 1 {
				comp.ChildPolicy = args[0].String()
			}
		default:
			comp.Body = append(comp.Body, p.parseVisualNode(child))
		}
	}
	return comp
}

func (p *parser) parseParam(node *kdl.Node) *ast.Param {
	args := node.Arguments()
	if len(args) < 2 {
		p.errorf("@param: expected name and default value arguments")
		return &ast.Param{}
	}
	param := &ast.Param{
		Pos:     p.pos(node),
		Name:    args[0].String(),
		Default: p.toExpr(args[1]),
	}
	if len(args) >= 3 && args[2].Kind() == kdl.String && args[2].String() == "required" {
		param.Required = true
	}
	return param
}

func (p *parser) parsePropDecl(node *kdl.Node) *ast.PropDecl {
	args := node.Arguments()
	if len(args) < 2 {
		p.errorf("@prop: expected name and type arguments")
		return &ast.PropDecl{Pos: p.pos(node)}
	}
	typeAnnotation, _ := args[1].TypeAnnotation()
	decl := &ast.PropDecl{
		Pos:      p.pos(node),
		Name:     args[0].String(),
		TypeHint: typeAnnotation,
	}
	children := node.Children()
	if children != nil {
		for _, child := range children.Nodes {
			if child.Name() == "@enum" {
				for _, arg := range child.Arguments() {
					decl.Enum = append(decl.Enum, arg.String())
				}
			}
		}
	}
	return decl
}

func (p *parser) parseEventDecl(node *kdl.Node) *ast.EventDecl {
	args := node.Arguments()
	if len(args) < 2 {
		p.errorf("@event: expected name and payload type arguments")
		return &ast.EventDecl{Pos: p.pos(node)}
	}
	return &ast.EventDecl{
		Pos:         p.pos(node),
		Name:        args[0].String(),
		PayloadType: args[1].String(),
	}
}

func (p *parser) parseStylesNode(node *kdl.Node) []*ast.StylePropDef {
	children := node.Children()
	if children == nil {
		return nil
	}
	var defs []*ast.StylePropDef
	for _, child := range children.Nodes {
		args := child.Arguments()
		if len(args) < 1 {
			p.errorf("styles: property %q missing type annotation", child.Name())
			continue
		}
		typeAnnotation, _ := args[0].TypeAnnotation()
		defs = append(defs, &ast.StylePropDef{
			Pos:      p.pos(child),
			Name:     child.Name(),
			TypeHint: typeAnnotation,
		})
	}
	return defs
}

func (p *parser) parseStyleDecl(node *kdl.Node) *ast.StyleDecl {
	args := node.Arguments()
	if len(args) < 1 {
		p.errorf("style: missing name argument")
		return nil
	}
	s := &ast.StyleDecl{
		Pos:   p.pos(node),
		Name:  args[0].String(),
		Props: make(map[string]ast.Expr),
	}
	children := node.Children()
	if children != nil {
		for _, child := range children.Nodes {
			childArgs := child.Arguments()
			if len(childArgs) >= 1 {
				s.Props[child.Name()] = p.toExpr(childArgs[0])
			}
		}
	}
	return s
}

func (p *parser) parseApp(node *kdl.Node) *ast.App {
	app := &ast.App{Pos: p.pos(node)}
	children := node.Children()
	if children == nil {
		return app
	}
	for _, child := range children.Nodes {
		app.Children = append(app.Children, p.parseVisualNode(child))
	}
	return app
}

func (p *parser) parseVisualNode(node *kdl.Node) *ast.VisualNode {
	vn := &ast.VisualNode{
		Pos:        p.pos(node),
		Component:  node.Name(),
		Props:      make(map[string]ast.Expr),
		Events:     make(map[string]ast.Expr),
		StyleAttrs: make(map[string]ast.Expr),
		StyleBlock: make(map[string]ast.Expr),
		AttrNodes:  make(map[string]*ast.AttrNode),
	}

	// Process properties (key=value attributes)
	for _, key := range node.PropertyOrder() {
		val := node.Properties()[key]
		expr := p.toExpr(val)

		switch {
		case key == "id":
			vn.ID = &expr
		case key == "key":
			vn.Key = &expr
		case key == "class":
			vn.Class = &expr
		case key == "if":
			vn.If = &expr
		case key == "for":
			vn.For = p.parseForClause(val)
		case key == "ref":
			vn.Ref = &expr
		case strings.HasPrefix(key, "on:"):
			vn.Events[strings.TrimPrefix(key, "on:")] = expr
		case strings.HasPrefix(key, "style."):
			vn.StyleAttrs[strings.TrimPrefix(key, "style.")] = expr
		default:
			vn.Props[key] = expr
		}
	}

	// Process children
	children := node.Children()
	if children == nil {
		return vn
	}
	for _, child := range children.Nodes {
		name := child.Name()
		if attrName, ok := strings.CutPrefix(name, "@"); ok {
			if attrName == "style" {
				p.parseStyleBlock(child, vn.StyleBlock)
			} else {
				vn.AttrNodes[attrName] = p.parseAttrNode(child)
			}
		} else {
			vn.Children = append(vn.Children, p.parseVisualNode(child))
		}
	}

	return vn
}

func (p *parser) parseForClause(val kdl.Value) *ast.ForClause {
	if val.Kind() != kdl.String {
		p.errorf("for: expected string value")
		return nil
	}
	parts := strings.SplitN(val.String(), " in ", 2)
	if len(parts) != 2 {
		p.errorf("for: expected format \"item in collection\"")
		return nil
	}
	iterable := p.parseCEL(strings.TrimSpace(parts[1]))
	varPart := strings.TrimSpace(parts[0])
	fc := &ast.ForClause{Iterable: iterable}
	if i := strings.Index(varPart, ","); i >= 0 {
		fc.Variable = strings.TrimSpace(varPart[:i])
		fc.IndexVar = strings.TrimSpace(varPart[i+1:])
	} else {
		fc.Variable = varPart
	}
	return fc
}

func (p *parser) parseStyleBlock(node *kdl.Node, block map[string]ast.Expr) {
	children := node.Children()
	if children == nil {
		return
	}
	for _, child := range children.Nodes {
		args := child.Arguments()
		if len(args) >= 1 {
			block[child.Name()] = p.toExpr(args[0])
		}
	}
}

func (p *parser) parseAttrNode(node *kdl.Node) *ast.AttrNode {
	an := &ast.AttrNode{
		Pos:   p.pos(node),
		Name:  strings.TrimPrefix(node.Name(), "@"),
		Props: make(map[string]ast.Expr),
	}
	for key, val := range node.Properties() {
		an.Props[key] = p.toExpr(val)
	}
	children := node.Children()
	if children != nil {
		for _, child := range children.Nodes {
			args := child.Arguments()
			if len(args) >= 1 {
				an.Props[child.Name()] = p.toExpr(args[0])
			}
		}
	}
	return an
}

func (p *parser) parseCEL(src string) ast.Expr {
	celAst, iss := p.celEnv.Parse(src)
	if iss != nil && iss.Err() != nil {
		p.errorf("CEL parse error in %q: %v", src, iss.Err())
		return ast.Expr{CEL: src}
	}
	return ast.Expr{CEL: src, AST: celAst}
}

func (p *parser) toExpr(val kdl.Value) ast.Expr {
	typeAnnotation, hasType := val.TypeAnnotation()

	if hasType && typeAnnotation == "cel" {
		return p.parseCEL(val.String())
	}

	var literal any
	switch val.Kind() {
	case kdl.String:
		literal = val.String()
	case kdl.Int:
		literal = val.Int()
	case kdl.Float:
		literal = val.Float()
	case kdl.Bool:
		literal = val.Bool()
	case kdl.Null:
		literal = nil
	default:
		literal = val.RawValue()
	}

	expr := ast.Expr{Literal: literal}
	if hasType {
		expr.TypeHint = typeAnnotation
	}
	return expr
}
