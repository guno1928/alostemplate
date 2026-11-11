package qt

import "io"

type Renderer struct {
	String func(v []string) string
	Write  func(w io.Writer, v []string)
}

var Renderers = map[string]Renderer{
	"Tiny":       {String: Tiny, Write: WriteTiny},
	"Small":      {String: Small, Write: WriteSmall},
	"Medium":     {String: Medium, Write: WriteMedium},
	"Large":      {String: Large, Write: WriteLarge},
	"Huge":       {String: Huge, Write: WriteHuge},
	"LongValues": {String: LongValues, Write: WriteLongValues},
	"ManySlots":  {String: ManySlots, Write: WriteManySlots},
}
