package pool

import (
	"bytes"
	"image"
	"image/color"
	"image/color/palette"
	"image/gif"
	"image/png"
	"net/http"
	"testing"
)

func TestStaticAvatarFlattensGIF(t *testing.T) {
	anim := &gif.GIF{}
	for _, c := range []color.Color{color.RGBA{255, 0, 0, 255}, color.RGBA{0, 0, 255, 255}} {
		frame := image.NewPaletted(image.Rect(0, 0, 32, 32), palette.Plan9)
		for i := range frame.Pix {
			frame.Pix[i] = uint8(frame.Palette.Index(c))
		}
		anim.Image, anim.Delay = append(anim.Image, frame), append(anim.Delay, 10)
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, anim); err != nil {
		t.Fatal(err)
	}
	out, kind, err := staticAvatar(buf.Bytes())
	if err != nil || kind != "image/png" || http.DetectContentType(out) != "image/png" {
		t.Fatalf("kind=%s err=%v", kind, err)
	}
	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if r, _, b, _ := img.At(5, 5).RGBA(); r>>8 != 255 || b>>8 != 0 {
		t.Fatalf("expected first red frame, got r=%d b=%d", r>>8, b>>8)
	}
	if _, _, err = staticAvatar([]byte("<svg></svg>")); err == nil {
		t.Fatal("svg should be rejected")
	}
}
