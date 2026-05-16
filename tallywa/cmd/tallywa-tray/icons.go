package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"runtime"
)

// iconForState returns a tray icon coloured for the given state. We
// generate the bitmap at runtime to avoid shipping binary blobs in the
// repo — the installer overlays a real artwork ICO before signing.
func iconForState(s uiState) []byte {
	var c color.RGBA
	switch s {
	case stateConnected:
		c = color.RGBA{R: 37, G: 211, B: 102, A: 255} // WhatsApp green
	case stateAwaitingQR, stateNotActivated:
		c = color.RGBA{R: 245, G: 166, B: 35, A: 255} // amber
	case stateLoggedOut, stateServiceDown:
		c = color.RGBA{R: 215, G: 58, B: 58, A: 255} // red
	default:
		c = color.RGBA{R: 122, G: 122, B: 122, A: 255} // grey
	}
	return iconBytes(c)
}

// iconBytes builds a 16×16 solid-colour icon. On Windows we need an
// ICO container; on macOS/Linux fyne.io/systray accepts PNG directly.
func iconBytes(c color.RGBA) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			img.Set(x, y, c)
		}
	}
	var pngBuf bytes.Buffer
	_ = png.Encode(&pngBuf, img)

	if runtime.GOOS != "windows" {
		return pngBuf.Bytes()
	}

	// ICONDIR (6 bytes) + ICONDIRENTRY (16 bytes) + PNG payload.
	var ico bytes.Buffer
	_ = binary.Write(&ico, binary.LittleEndian, uint16(0)) // reserved
	_ = binary.Write(&ico, binary.LittleEndian, uint16(1)) // type ICO
	_ = binary.Write(&ico, binary.LittleEndian, uint16(1)) // image count
	ico.WriteByte(16)                                      // width
	ico.WriteByte(16)                                      // height
	ico.WriteByte(0)                                       // palette
	ico.WriteByte(0)                                       // reserved
	_ = binary.Write(&ico, binary.LittleEndian, uint16(1))                       // planes
	_ = binary.Write(&ico, binary.LittleEndian, uint16(32))                      // bpp
	_ = binary.Write(&ico, binary.LittleEndian, uint32(pngBuf.Len()))            // image size
	_ = binary.Write(&ico, binary.LittleEndian, uint32(22))                      // offset
	ico.Write(pngBuf.Bytes())
	return ico.Bytes()
}
