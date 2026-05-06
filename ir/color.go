package ir

// Color tracks the synchronous-vs-asynchronous flavor of a function or
// function-typed slot. Sync (zero value) or Async are concrete; Param
// indicates the color depends on a funcvar parameter, with the index
// recorded on FuncSig.PolyParam.
type Color int

const (
	ColorSync Color = iota
	ColorAsync
	ColorParam
)

func (c Color) String() string {
	switch c {
	case ColorSync:
		return "Sync"
	case ColorAsync:
		return "Async"
	case ColorParam:
		return "Param"
	default:
		return "ColorUnknown"
	}
}
