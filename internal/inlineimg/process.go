package inlineimg

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"image"
	_ "image/gif" // registers the GIF decoder for image.DecodeConfig
	"image/jpeg"
	"image/png"

	xdraw "golang.org/x/image/draw"
)

const (
	// MaxDisplayWidth is the widest an image is shown, in CSS pixels: the usual
	// width of an email column. A larger image is scaled down to it.
	MaxDisplayWidth = 600
	// embedScale is how many pixels are embedded per displayed CSS pixel, so the
	// image stays sharp on high-DPI screens.
	embedScale = 2
	// MaxEmbedWidth is the ceiling on embedded pixel width.
	MaxEmbedWidth = 2000
	// MaxFileBytes limits one image, before and after processing.
	MaxFileBytes = 20 << 20
	// MaxTotalBytes limits all inline images of a message together.
	MaxTotalBytes = 20 << 20
	// jpegQuality is the quality JPEGs are re-encoded at.
	jpegQuality = 85
)

// processed is one image ready to embed.
type processed struct {
	data        []byte
	contentType string
	ext         string
	// width and height are the display size in CSS pixels.
	width, height int
}

// process decodes data, and prepares it for embedding. wantWidth is the
// requested display width in CSS pixels (0 for automatic). Images are never
// enlarged. JPEGs are always re-encoded, which applies the EXIF orientation and
// drops all metadata (including GPS position). PNG is re-encoded only when it
// has to be shrunk. GIF is passed through untouched so animation survives.
func process(data []byte, wantWidth int) (processed, error) {
	if len(data) > MaxFileBytes {
		return processed{}, fmt.Errorf("image is %d MB; the limit is %d MB", len(data)>>20, MaxFileBytes>>20)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return processed{}, fmt.Errorf("not a PNG, JPEG or GIF image (%v)", err)
	}
	if format != "png" && format != "jpeg" && format != "gif" {
		return processed{}, fmt.Errorf("%s images are not supported; use PNG, JPEG or GIF", format)
	}
	orientation := 1
	if format == "jpeg" {
		orientation = jpegOrientation(data)
	}
	natW, natH := cfg.Width, cfg.Height
	if orientation >= 5 {
		natW, natH = natH, natW // the display is rotated a quarter turn
	}
	if natW <= 0 || natH <= 0 {
		return processed{}, fmt.Errorf("image has no size")
	}
	displayW := min(natW, MaxDisplayWidth)
	if wantWidth > 0 {
		displayW = min(natW, wantWidth) // never enlarge
	}
	displayH := max(1, (displayW*natH+natW/2)/natW)
	embedW := min(natW, min(displayW*embedScale, MaxEmbedWidth))

	out := processed{width: displayW, height: displayH}
	switch format {
	case "gif":
		out.data, out.contentType, out.ext = data, "image/gif", "gif"
	case "png":
		out.contentType, out.ext = "image/png", "png"
		if embedW >= natW {
			out.data = data
			break
		}
		img, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			return processed{}, fmt.Errorf("cannot decode the PNG: %v", err)
		}
		var buf bytes.Buffer
		enc := png.Encoder{CompressionLevel: png.BestCompression}
		if err := enc.Encode(&buf, scale(img, embedW)); err != nil {
			return processed{}, err
		}
		out.data = buf.Bytes()
	case "jpeg":
		out.contentType, out.ext = "image/jpeg", "jpg"
		img, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			return processed{}, fmt.Errorf("cannot decode the JPEG: %v", err)
		}
		img = orient(img, orientation)
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, scale(img, embedW), &jpeg.Options{Quality: jpegQuality}); err != nil {
			return processed{}, err
		}
		out.data = buf.Bytes()
	}
	if len(out.data) > MaxFileBytes {
		return processed{}, fmt.Errorf("image is %d MB after processing; the limit is %d MB", len(out.data)>>20, MaxFileBytes>>20)
	}
	return out, nil
}

// scale shrinks img to width w, keeping its proportions. It never enlarges.
func scale(img image.Image, w int) image.Image {
	b := img.Bounds()
	if w <= 0 || w >= b.Dx() {
		return img
	}
	h := max(1, (w*b.Dy()+b.Dx()/2)/b.Dx())
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, b, xdraw.Src, nil)
	return dst
}

// contentID derives a stable Content-ID from the embedded bytes, so the same
// image at the same size is one part however often it is referenced.
func contentID(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x@tidemail.local", sum[:12])
}

// jpegOrientation returns the EXIF orientation (1-8) of a JPEG, or 1 when there
// is none or it cannot be read.
func jpegOrientation(b []byte) int {
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 {
		return 1
	}
	i := 2
	for i+4 <= len(b) {
		if b[i] != 0xFF {
			return 1
		}
		marker := b[i+1]
		switch {
		case marker == 0xFF:
			i++ // fill byte
			continue
		case marker == 0xD9 || marker == 0xDA:
			return 1 // end of image or start of scan: no more headers
		case marker == 0x01 || (marker >= 0xD0 && marker <= 0xD7):
			i += 2 // standalone markers carry no length
			continue
		}
		segLen := int(b[i+2])<<8 | int(b[i+3])
		if segLen < 2 || i+2+segLen > len(b) {
			return 1
		}
		if marker == 0xE1 && segLen >= 8 && string(b[i+4:i+10]) == "Exif\x00\x00" {
			return tiffOrientation(b[i+10 : i+2+segLen])
		}
		i += 2 + segLen
	}
	return 1
}

func tiffOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var bo binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 1
	}
	if bo.Uint16(t[2:4]) != 42 {
		return 1
	}
	off := int(bo.Uint32(t[4:8]))
	if off < 8 || off+2 > len(t) {
		return 1
	}
	n := int(bo.Uint16(t[off:]))
	for k := 0; k < n; k++ {
		e := off + 2 + k*12
		if e+12 > len(t) {
			return 1
		}
		if bo.Uint16(t[e:]) == 0x0112 { // Orientation, type SHORT with the value inline
			if v := int(bo.Uint16(t[e+8:])); v >= 1 && v <= 8 {
				return v
			}
			return 1
		}
	}
	return 1
}

// orient returns img transformed so that it displays upright, given its EXIF
// orientation.
func orient(img image.Image, o int) image.Image {
	if o <= 1 || o > 8 {
		return img
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	dw, dh := w, h
	if o >= 5 {
		dw, dh = h, w
	}
	dst := image.NewNRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var nx, ny int
			switch o {
			case 2: // mirrored horizontally
				nx, ny = w-1-x, y
			case 3: // rotated 180
				nx, ny = w-1-x, h-1-y
			case 4: // mirrored vertically
				nx, ny = x, h-1-y
			case 5: // transposed
				nx, ny = y, x
			case 6: // rotated 90 clockwise
				nx, ny = h-1-y, x
			case 7: // transverse
				nx, ny = h-1-y, w-1-x
			case 8: // rotated 90 counter-clockwise
				nx, ny = y, w-1-x
			}
			dst.Set(nx, ny, img.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}
