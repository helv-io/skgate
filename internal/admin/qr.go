package admin

import (
	"fmt"
	"html/template"
	"net/url"
	"strings"

	"rsc.io/qr"

	"github.com/helv-io/skgate/internal/provider"
)

// deviceLink is the verification address without the user code. The device panel opens it,
// the fallback link shows it in full, and the QR encodes it. verification_uri is used as
// returned. The complete URI is only a fallback, and its user_code is stripped.
func deviceLink(d provider.DeviceFlow) string {
	if u := strings.TrimSpace(d.VerificationURI); u != "" {
		return u
	}
	return stripUserCode(d.VerificationURIComplete)
}

func stripUserCode(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	q := u.Query()
	q.Del("user_code")
	u.RawQuery = q.Encode()
	return u.String()
}

// qrSVG draws a QR of text as one SVG. The quiet zone is four modules, the spec minimum.
// Empty text or a string that will not fit yields nothing.
func qrSVG(text string) template.HTML {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	code, err := qr.Encode(text, qr.M)
	if err != nil || code.Size < 1 {
		return ""
	}
	const quiet = 4
	n := code.Size + quiet*2
	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="qr" viewBox="0 0 %d %d" role="img" aria-label="Sign-in QR">`, n, n)
	fmt.Fprintf(&b, `<rect width="%d" height="%d" fill="#fff"/>`, n, n)
	b.WriteString(`<path fill="#111" d="`)
	for y := 0; y < code.Size; y++ {
		for x := 0; x < code.Size; x++ {
			if code.Black(x, y) {
				fmt.Fprintf(&b, "M%d %dh1v1h-1z", x+quiet, y+quiet)
			}
		}
	}
	b.WriteString(`"/></svg>`)
	return template.HTML(b.String())
}
