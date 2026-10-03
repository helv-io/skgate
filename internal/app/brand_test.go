package app

import (
	"bytes"
	"encoding/binary"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func readStatic(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "admin", "static", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// One logo: the README image is the favicon, and every raster in static/ is that logo at its size
// (dark rounded square, yellow K).
func TestBrandAssetsAreOneLogo(t *testing.T) {
	svg := readStatic(t, "favicon.svg")
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "img", "logo.svg"))
	if err != nil || !bytes.Equal(svg, doc) {
		t.Errorf("docs/img/logo.svg must be the favicon (err=%v)", err)
	}
	for _, old := range []string{"halo", "plane", `class="bg"`, "#111318", "#FFD100"} {
		if bytes.Contains(svg, []byte(old)) {
			t.Errorf("favicon.svg still has the old logo markup %q", old)
		}
	}
	for _, want := range []string{"rgb(17,19,24)", "rgb(255,209,0)", `viewBox="0 0 64 64"`} {
		if !bytes.Contains(svg, []byte(want)) {
			t.Errorf("favicon.svg lacks %q", want)
		}
	}
	for name, size := range map[string]int{"favicon-16.png": 16, "favicon-32.png": 32, "favicon-48.png": 48, "favicon-192.png": 192, "favicon-512.png": 512, "apple-touch-icon.png": 180} {
		img, err := png.Decode(bytes.NewReader(readStatic(t, name)))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if b := img.Bounds(); b.Dx() != size || b.Dy() != size {
			t.Errorf("%s is %dx%d, want %d", name, b.Dx(), b.Dy(), size)
		}
		if size < 64 {
			continue
		}
		at := func(x, y float64) color.NRGBA {
			return color.NRGBAModel.Convert(img.At(int(x/64*float64(size)), int(y/64*float64(size)))).(color.NRGBA)
		}
		if c := at(14, 32); c.R != 255 || c.G < 200 || c.G > 215 || c.B > 8 {
			t.Errorf("%s: the K is %v, want rgb(255,209,0)", name, c)
		}
		if c := at(56, 8); c.R > 20 || c.G < 15 || c.G > 24 || c.B < 20 || c.B > 28 || c.A != 255 {
			t.Errorf("%s: the background is %v, want rgb(17,19,24)", name, c)
		}
		if corner := at(0.2, 0.2); name != "apple-touch-icon.png" && corner.A != 0 {
			t.Errorf("%s: corners should be transparent, got %v", name, corner)
		}
	}
	ico := readStatic(t, "favicon.ico")
	if len(ico) < 22 || binary.LittleEndian.Uint16(ico[2:]) != 1 {
		t.Fatal("favicon.ico is not an icon file")
	}
	var got []int
	for i := 0; i < int(binary.LittleEndian.Uint16(ico[4:])); i++ {
		w := int(ico[6+16*i])
		if w == 0 {
			w = 256
		}
		got = append(got, w)
	}
	if len(got) != 4 || got[0] != 16 || got[1] != 32 || got[2] != 48 || got[3] != 256 {
		t.Errorf("favicon.ico sizes %v, want 16 32 48 256", got)
	}
}
