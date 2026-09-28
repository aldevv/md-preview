//go:build darwin

package nativewin

import "github.com/ebitengine/purego/objc"

// buildDockIcon renders a small placeholder document glyph (dark badge,
// light page, three text-line bars) entirely out of NSBezierPath rects
// so the native window gets a distinct Dock icon instead of the generic
// terminal/Go-binary one. Returns 0 if any Cocoa class lookup fails.
func buildDockIcon() objc.ID {
	const size = 256.0

	img := objc.ID(objc.GetClass("NSImage")).Send(objc.RegisterName("alloc"))
	img = img.Send(objc.RegisterName("initWithSize:"), nsSize{W: size, H: size})
	if img == 0 {
		return 0
	}

	img.Send(objc.RegisterName("lockFocus"))
	fillRoundedRect(nsRect{Size: nsSize{W: size, H: size}}, 48, 0.141, 0.149, 0.169)
	fillRoundedRect(nsRect{Origin: nsPoint{X: 64, Y: 40}, Size: nsSize{W: 128, H: 176}}, 12, 0.961, 0.961, 0.961)
	for _, y := range []float64{150, 120, 90} {
		fillRect(nsRect{Origin: nsPoint{X: 84, Y: y}, Size: nsSize{W: 88, H: 14}}, 0.431, 0.463, 0.506)
	}
	img.Send(objc.RegisterName("unlockFocus"))

	return img
}

func nsColor(r, g, b float64) objc.ID {
	return objc.ID(objc.GetClass("NSColor")).Send(objc.RegisterName("colorWithSRGBRed:green:blue:alpha:"), r, g, b, float64(1))
}

func fillRoundedRect(rect nsRect, radius, r, g, b float64) {
	nsColor(r, g, b).Send(objc.RegisterName("set"))
	path := objc.ID(objc.GetClass("NSBezierPath")).Send(objc.RegisterName("bezierPathWithRoundedRect:xRadius:yRadius:"), rect, radius, radius)
	path.Send(objc.RegisterName("fill"))
}

func fillRect(rect nsRect, r, g, b float64) {
	nsColor(r, g, b).Send(objc.RegisterName("set"))
	path := objc.ID(objc.GetClass("NSBezierPath")).Send(objc.RegisterName("bezierPathWithRect:"), rect)
	path.Send(objc.RegisterName("fill"))
}
