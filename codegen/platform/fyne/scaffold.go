package fyne

import (
	"embed"
	"fmt"
	"slices"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
)

//go:embed templates/*
var templateFS embed.FS

// templateData is the data passed to the model.go.tmpl template.
type templateData struct {
	Package      string
	GenerateMain bool
	AppName      string
	NeedsTime    bool
	NeedsURL     bool
	NeedsCanvas  bool
	NeedsToast   bool
	HasTimers    bool

	Structs      []structData
	Binds        []bindData
	Externs      []externData
	Computeds    []computedData
	Entries      []entryData
	WidgetFields []widgetFieldData
	Timers       []timerData
	UpdaterNames []string
	FunctionCode string   // pre-rendered user functions
	GoImports    []string // native Go import paths from Resolved fields
}

type structData struct {
	Name   string
	Fields []structFieldData
}

type structFieldData struct {
	Name string
	Type string
}

type bindData struct {
	Name        string
	GoType      string
	InitVal     string
	Getter      string
	SetterExtra string // pre-rendered extra setter lines (entry sync, trigger, updaters)
}

type externData struct {
	Name   string
	GoType string
}

type computedData struct {
	Name   string
	GoType string
	Body   string // pre-rendered expression
}

type entryData struct {
	FieldName         string
	MultiLine         bool
	Password          bool
	Placeholder       string
	PlaceholderQuoted string
	BindTarget        string
	OnChangedBody     string // pre-rendered updater calls
	Rows              int
}

type widgetFieldData struct {
	Name   string
	GoType string
}

type timerData struct {
	Index            int
	IntervalMs       int
	ActiveVar        string
	Body             string // pre-rendered mutation statements
	AffectedUpdaters string // pre-rendered updater calls
}

func newTemplateData(info *analysisResult, cfg Config, updaters []widgetUpdater, widgetFields []widgetField, functionCode string, ec *exprContext, doc *ast.Document) templateData {
	td := templateData{
		Package:      cfg.Package,
		GenerateMain: cfg.GenerateMain,
		AppName:      cfg.AppName,
		NeedsTime:    info.needsTime,
		NeedsURL:     info.needsURL,
		NeedsCanvas:  info.needsCanvas,
		NeedsToast:   info.NeedsToast,
		HasTimers:    len(info.Timers) > 0,
		FunctionCode: functionCode,
	}

	// Native Go imports from Resolved fields
	for pkg := range info.goImports {
		td.GoImports = append(td.GoImports, pkg)
	}
	slices.Sort(td.GoImports)

	// Structs
	for _, sd := range info.Structs {
		s := structData{Name: exportName(sd.Name)}
		for _, f := range sd.Fields {
			goType := typeHintToGo(f.Type)
			if f.Resolved != nil && f.Resolved.NativeType != "" {
				goType = f.Resolved.NativeType
			}
			s.Fields = append(s.Fields, structFieldData{
				Name: exportName(f.Name),
				Type: goType,
			})
		}
		td.Structs = append(td.Structs, s)
	}

	// Binds
	for _, bind := range info.binds {
		getter := exportName(bind.name)
		bd := bindData{
			Name:    bind.name,
			GoType:  bind.goType,
			InitVal: bind.initVal,
			Getter:  getter,
		}
		// Build setter extra lines
		var extra strings.Builder
		for _, entry := range info.entries {
			if entry.bindTarget == bind.name && bind.goType == "string" {
				fmt.Fprintf(&extra, "\tm.%s.SetText(v)\n", entry.fieldName)
			}
		}
		// Pre-render @change event bodies for this data field.
		if events, ok := info.dataEvents[bind.name]; ok {
			for _, ev := range events {
				if ev.Kind == "change" {
					stmts := ec.TranslateMutation(ev.Body)
					for _, s := range stmts {
						fmt.Fprintf(&extra, "\t%s\n", s)
					}
				}
			}
		}
		mutated := map[string]bool{bind.name: true}
		affected := codegen.FindAffected(info.depTracker(), updaters, mutated)
		for _, u := range affected {
			fmt.Fprintf(&extra, "\tm.%s()\n", u.name)
		}
		bd.SetterExtra = extra.String()
		td.Binds = append(td.Binds, bd)
	}

	// Externs
	for _, ext := range info.externs {
		td.Externs = append(td.Externs, externData{
			Name:   exportName(ext.name),
			GoType: ext.goType,
		})
	}

	// Widget fields
	for _, wf := range widgetFields {
		td.WidgetFields = append(td.WidgetFields, widgetFieldData{
			Name:   wf.name,
			GoType: wf.goType,
		})
	}

	// Updater names (for doRefresh)
	for _, u := range updaters {
		td.UpdaterNames = append(td.UpdaterNames, u.name)
	}

	// Entries
	for _, entry := range info.entries {
		ed := entryData{
			FieldName:   entry.fieldName,
			MultiLine:   entry.multiLine,
			Password:    entry.password,
			Placeholder: entry.placeholder,
			BindTarget:  entry.bindTarget,
			Rows:        entry.rows,
		}
		if entry.placeholder != "" {
			ed.PlaceholderQuoted = fmt.Sprintf("%q", entry.placeholder)
		}
		if entry.bindTarget != "" {
			var body strings.Builder
			entryMutated := map[string]bool{entry.bindTarget: true}
			affected := codegen.FindAffected(info.depTracker(), updaters, entryMutated)
			if len(affected) > 0 {
				for _, u := range affected {
					fmt.Fprintf(&body, "\t\tm.%s()\n", u.name)
				}
			} else {
				body.WriteString("\t\tm.doRefresh()\n")
			}
			ed.OnChangedBody = body.String()
		}
		td.Entries = append(td.Entries, ed)
	}

	return td
}
