package fyne

// irWidgetField tracks a persistent widget stored on Model.
type irWidgetField struct {
	name   string
	goType string
}

type entrySyncRec struct {
	varName   string // sngl var to sync from
	fieldName string // Model field of the widget
	target    string // method to call, e.g. ".SetText"
}
