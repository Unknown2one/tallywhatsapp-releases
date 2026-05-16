package httpapi

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"

	"rsc.io/qr"
)

// encodeQRDataURL turns a whatsmeow pairing string into a "data:image/png"
// URL the dashboard can plug straight into <img src>.
//
// We render at module size 8 (so the QR matrix's smallest cell is an 8×8
// block of pixels). That gives a final image ~250 px wide for a typical
// WhatsApp pairing string — large enough to scan from a phone held at
// arm's length but small enough to fit the dashboard layout without
// scaling artefacts.
//
// rsc.io/qr is pure-Go and ~30 KB compiled — preferable to a CGO QR
// library when we already ship one binary that has to behave on every
// SMB Windows box.
func encodeQRDataURL(content string) (string, error) {
	code, err := qr.Encode(content, qr.M)
	if err != nil {
		return "", err
	}
	const block = 8
	size := code.Size * block
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	white := color.RGBA{0xff, 0xff, 0xff, 0xff}
	black := color.RGBA{0x00, 0x00, 0x00, 0xff}
	for y := 0; y < code.Size; y++ {
		for x := 0; x < code.Size; x++ {
			c := white
			if code.Black(x, y) {
				c = black
			}
			for dy := 0; dy < block; dy++ {
				for dx := 0; dx < block; dx++ {
					img.Set(x*block+dx, y*block+dy, c)
				}
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}
