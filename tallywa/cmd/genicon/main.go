// genicon produces a Windows .ico file containing the TallyWhatsApp brand
// mark — a rounded green tile with a white "T". Runs as a build-time
// helper so we don't check in a binary asset; lets the install/Start
// Menu/Desktop shortcuts all share the same artwork.
//
// We emit a single 256×256 PNG-in-ICO (Vista+ format), which Explorer
// happily downscales for 16/32/48 px contexts. Writing a true multi-
// resolution ICO would be ~50 lines more for zero perceptual win on
// modern Windows.
//
// Usage:
//
//	go run ./cmd/genicon -out path\to\icon.ico
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"
)

const (
	size    = 256
	radius  = 56 // rounded-corner radius, matching the favicon's 14/64 ratio scaled to 256.
)

var (
	greenTop = color.RGBA{R: 0x4a, G: 0xe6, B: 0x83, A: 0xff} // brand green, lit
	greenBot = color.RGBA{R: 0x12, G: 0x8a, B: 0x3f, A: 0xff} // brand green, deep
	letterFG = color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
)

func main() {
	out := flag.String("out", "icon.ico", "output .ico path")
	flag.Parse()

	img := image.NewRGBA(image.Rect(0, 0, size, size))

	// 1. Vertical gradient.
	for y := 0; y < size; y++ {
		t := float64(y) / float64(size-1)
		c := color.RGBA{
			R: lerp(greenTop.R, greenBot.R, t),
			G: lerp(greenTop.G, greenBot.G, t),
			B: lerp(greenTop.B, greenBot.B, t),
			A: 0xff,
		}
		for x := 0; x < size; x++ {
			img.SetRGBA(x, y, c)
		}
	}

	// 2. Carve rounded corners by zeroing alpha outside the rounded-rect mask.
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			if !insideRoundRect(x, y, size, size, radius) {
				img.SetRGBA(x, y, color.RGBA{})
			}
		}
	}

	// 3. Draw a chunky letter T centred on the tile.
	drawLetterT(img)

	// 4. Encode PNG, wrap in ICO container.
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		log.Fatalf("png encode: %v", err)
	}
	ico, err := wrapAsICO(pngBuf.Bytes())
	if err != nil {
		log.Fatalf("ico wrap: %v", err)
	}
	if err := os.WriteFile(*out, ico, 0o644); err != nil {
		log.Fatalf("write %s: %v", *out, err)
	}
	fmt.Printf("wrote %s (%d bytes, 256x256 PNG-in-ICO)\n", *out, len(ico))
}

func lerp(a, b uint8, t float64) uint8 {
	return uint8(math.Round(float64(a)*(1-t) + float64(b)*t))
}

func insideRoundRect(x, y, w, h, r int) bool {
	if x >= r && x < w-r {
		return y >= 0 && y < h
	}
	if y >= r && y < h-r {
		return x >= 0 && x < w
	}
	cx, cy := r, r
	if x >= w-r {
		cx = w - r - 1
	}
	if y >= h-r {
		cy = h - r - 1
	}
	dx := x - cx
	dy := y - cy
	return dx*dx+dy*dy <= r*r
}

// drawLetterT lays down the brand "T" using a hand-rolled rectangle pair
// instead of a real font. Reason: basicfont's "T" is too thin for the
// brand. A 24-px-wide bar + 24-px-wide cross is the same proportion as
// the favicon SVG and renders cleanly down to 16×16.
func drawLetterT(img *image.RGBA) {
	// Top crossbar.
	const (
		barH      = 32  // crossbar thickness
		barW      = 144 // crossbar width
		stemW     = 32  // stem thickness
		stemH     = 144 // stem height
		topMargin = 56  // distance from top of icon to top of crossbar
	)
	cx := size / 2
	x0 := cx - barW/2
	x1 := cx + barW/2
	y0 := topMargin
	y1 := topMargin + barH
	fillRect(img, x0, y0, x1, y1, letterFG)

	// Stem hangs from the centre of the crossbar.
	sx0 := cx - stemW/2
	sx1 := cx + stemW/2
	sy0 := topMargin + barH
	sy1 := sy0 + stemH
	fillRect(img, sx0, sy0, sx1, sy1, letterFG)
}

func fillRect(img *image.RGBA, x0, y0, x1, y1 int, c color.RGBA) {
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			img.SetRGBA(x, y, c)
		}
	}
}

// wrapAsICO packages a PNG byte stream into an ICO container.
// Format reference: ICONDIR (6 bytes) + ICONDIRENTRY (16 bytes) + payload.
// width/height fields use 0 to mean 256 (the trick the Windows SDK uses).
func wrapAsICO(pngBytes []byte) ([]byte, error) {
	var buf bytes.Buffer

	// ICONDIR
	binary.Write(&buf, binary.LittleEndian, uint16(0))      // reserved
	binary.Write(&buf, binary.LittleEndian, uint16(1))      // type = icon
	binary.Write(&buf, binary.LittleEndian, uint16(1))      // image count

	// ICONDIRENTRY
	binary.Write(&buf, binary.LittleEndian, uint8(0))               // width  (0 = 256)
	binary.Write(&buf, binary.LittleEndian, uint8(0))               // height (0 = 256)
	binary.Write(&buf, binary.LittleEndian, uint8(0))               // colour palette
	binary.Write(&buf, binary.LittleEndian, uint8(0))               // reserved
	binary.Write(&buf, binary.LittleEndian, uint16(1))              // colour planes
	binary.Write(&buf, binary.LittleEndian, uint16(32))             // bits per pixel
	binary.Write(&buf, binary.LittleEndian, uint32(len(pngBytes)))  // payload size
	binary.Write(&buf, binary.LittleEndian, uint32(6+16))           // payload offset

	buf.Write(pngBytes)
	return buf.Bytes(), nil
}
