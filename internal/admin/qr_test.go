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

func TestDeviceOpenCarriesTheUserCode(t *testing.T) {
	const (
		uri  = "https://accounts.x.ai/oauth2/device"
		code = "ABCD-1234"
	)
	built := uri + "?user_code=" + code
	d := provider.DeviceFlow{UserCode: code, VerificationURI: uri, VerificationURIComplete: built}
	if got := deviceOpen(d); got != built {
		t.Fatalf("complete URI: %q", got)
	}
	// A provider that names the code its own way is opened as it asked, not rebuilt.
	odd := "https://login.example/device?otc=" + code
	d.VerificationURIComplete = odd
	if got := deviceOpen(d); got != odd {
		t.Fatalf("provider complete URI wins: %q", got)
	}
	d.VerificationURIComplete = "  "
	if got := deviceOpen(d); got != built {
		t.Fatalf("blank complete URI is built: %q", got)
	}
	d.VerificationURIComplete = ""
	if got := deviceOpen(d); got != built {
		t.Fatalf("missing complete URI is built: %q", got)
	}
	d.UserCode = ""
	if got := deviceOpen(d); got != uri {
		t.Fatalf("no code falls back to the plain address: %q", got)
	}
	got := deviceOpen(provider.DeviceFlow{UserCode: "A&B", VerificationURI: uri + "?foo=1"})
	if want := uri + "?foo=1&user_code=A%26B"; got != want {
		t.Fatalf("append: got %q want %q", got, want)
	}
	if got := deviceOpen(provider.DeviceFlow{UserCode: code, VerificationURI: "not a url"}); got != "not a url" {
		t.Fatalf("unparseable address falls back: %q", got)
	}
	if deviceOpen(provider.DeviceFlow{}) != "" {
		t.Fatal("no address")
	}
	plain := deviceLink(provider.DeviceFlow{UserCode: code, VerificationURI: uri, VerificationURIComplete: built})
	if plain != uri {
		t.Fatalf("link text: %q", plain)
	}
	if qrSVG(built) == qrSVG(uri) {
		t.Fatal("the QR of the address with the code must differ from the plain address")
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
