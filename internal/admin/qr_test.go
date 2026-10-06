package admin

import (
	"strconv"
	"strings"
	"testing"

	"rsc.io/qr"

	"github.com/helv-io/skgate/internal/provider"
)

func TestDeviceLinkOmitsTheUserCode(t *testing.T) {
	const uri = "https://accounts.x.ai/oauth2/device"
	if got := deviceLink(provider.DeviceFlow{VerificationURI: uri, VerificationURIComplete: uri + "?user_code=ABCD-1234"}); got != uri {
		t.Fatalf("verification_uri wins: %q", got)
	}
	got := deviceLink(provider.DeviceFlow{VerificationURIComplete: uri + "?user_code=ABCD-1234&other=1"})
	if strings.Contains(got, "user_code") || !strings.HasPrefix(got, uri) || !strings.Contains(got, "other=1") {
		t.Fatalf("complete URI must lose only the code: %q", got)
	}
	if deviceLink(provider.DeviceFlow{}) != "" {
		t.Fatal("no address")
	}
}

func TestQRSVgDrawsTheAddress(t *testing.T) {
	const uri = "https://accounts.x.ai/oauth2/device"
	svg := string(qrSVG(uri))
	code, err := qr.Encode(uri, qr.M)
	if err != nil {
		t.Fatal(err)
	}
	n := strconv.Itoa(code.Size + 8) // four modules of quiet zone on each side
	for _, want := range []string{`class="qr"`, `aria-label="Sign-in QR"`, `fill="#fff"`, `fill="#111"`, `viewBox="0 0 ` + n + ` ` + n + `"`} {
		if !strings.Contains(svg, want) {
			t.Errorf("svg lacks %q", want)
		}
	}
	if strings.Contains(svg, uri) || strings.Contains(svg, "<script") || strings.Contains(svg, "style=") {
		t.Fatal("the svg must be modules only")
	}
	if qrSVG("") != "" || qrSVG("   ") != "" {
		t.Fatal("empty text draws nothing")
	}
	if qrSVG(uri+"?user_code=nope") == qrSVG(uri) {
		t.Fatal("a different address must draw a different code")
	}
}
