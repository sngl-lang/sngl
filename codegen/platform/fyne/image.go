package fyne

import (
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// fyne.sngl's image primitives, which are this platform's runtime and not Fyne
// widgets, so their construction is written here as Toplevel's is. An Image
// holds what it shows in a box: the source, which shows the picture itself, or
// the placeholder. A URL source loads what the URL names; a drawn one is a
// canvas whose picture is handed to it each time it paints.
const (
	imageTag        = "Image"
	imageURLTag     = "ImageUrl"
	imageDrawingTag = "ImageDrawing"
)

func imageSpecFor(tag string, props map[string]ir.Expr) *fyneSpec {
	sp := &fyneSpec{CtorProps: props, Currents: map[string]string{}, Setters: map[string]string{}, Handlers: map[string]fyneHandler{}}
	switch tag {
	case imageTag:
		sp.New = fyneNative{Path: fyneLayoutImportPath, Name: "NewImage"}
		sp.GoType = fyneNative{Path: fyneLayoutImportPath, Name: "*Image"}
		sp.Add = "Add"
		sp.slotBox = "Box"
		sp.Setters["fit"] = "SetFit"
		sp.Handlers["click"] = fyneHandler{Field: "OnTapped", Signature: "func()"}
		sp.Handlers["load"] = fyneHandler{Field: "OnLoad", Signature: "func()"}
		sp.Handlers["failed"] = fyneHandler{Field: "OnError", Signature: "func(message string)"}
	case imageURLTag:
		sp.New = fyneNative{Path: fyneLayoutImportPath, Name: "NewImageSource"}
		sp.GoType = fyneNative{Path: fyneLayoutImportPath, Name: "*ImageSource"}
		sp.Setters[ir.ImageSourceURL] = "SetURL"
	case imageDrawingTag:
		// Its size is the canvas it paints into, read where it is created.
		sp.New = fyneNative{Path: fyneLayoutImportPath, Name: "NewImageSource"}
		sp.GoType = fyneNative{Path: fyneLayoutImportPath, Name: "*ImageSource"}
		sp.CtorOnly = map[string]bool{"width": true, "height": true}
	default:
		return nil
	}
	return sp
}

func isImageDrawing(n *ir.NodeInst) bool {
	return n != nil && n.Component != nil && n.Component.Name == imageDrawingTag && fynePrimitive(n.Component) != ""
}

// emitImageDrawingCreate is a drawn source: the source, and the picture its
// drawing paints handed to it.
func (t *fyneTranslator) emitImageDrawingCreate(id string) []ir.Stmt {
	m := t.canvasByID[id]
	t.topLevel = append(t.topLevel, id)
	t.fieldIDs[id] = true
	t.fieldSink(id, "*fynelayout.ImageSource")
	t.fieldSink(canvasCtxField(id), "*"+snglCanvasAlias+".Context")
	if t.importSink != nil {
		t.importSink(snglCanvasImportPath)
		t.importSink(fyneLayoutImportPath)
	}
	create := &ir.Assign{
		Target: t.fieldRef(id),
		Op:     ast.AssignSet,
		Value:  nativeCallAt("fynelayout.NewImageSource", fyneLayoutImportPath, nil, ir.TypDyn),
	}
	return append([]ir.Stmt{create}, t.paintImageDrawing(m)...)
}

// paintImageDrawing paints a drawn source's picture afresh and hands it over.
func (t *fyneTranslator) paintImageDrawing(m *canvasMeta) []ir.Stmt {
	dcField := t.fieldRef(canvasCtxField(m.ID))
	w, h := canvasDims(m)
	return []ir.Stmt{
		&ir.Assign{Target: dcField, Op: ast.AssignSet, Value: newCanvasContextCall(w, h)},
		&ir.CallStmt{Call: &ir.Call{
			Type:     ir.TypVoid,
			Receiver: t.recvIdent(),
			Func:     &ir.Func{Name: m.DrawName},
			Args:     []ir.CallArg{{Value: dcField}},
		}},
		&ir.CallStmt{Call: methodCall(t.fieldRef(m.ID), "SetImage", []ir.Expr{methodCall(dcField, "Result", nil, ir.TypDyn)}, ir.TypVoid)},
	}
}
