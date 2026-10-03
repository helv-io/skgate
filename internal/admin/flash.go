package admin

import (
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"
)

// Flash messages survive exactly one redirect (POST-redirect-GET). They travel in a short-lived,
// HttpOnly, HMAC-signed cookie and are consumed (and cleared) by the next rendered page, so no
// message ever appears in a URL.

const (
	flashCookie = "skgate_flash"
	flashTTL    = time.Minute
	flashMax    = 300
)

// Toast kinds; the CSS class of the rendered toast.
const (
	toastOK  = "ok"
	toastBad = "bad"
)

type toast struct {
	Kind string `json:"k"`
	Msg  string `json:"m"`
}

func clip(s string) string {
	if r := []rune(s); len(r) > flashMax {
		return string(r[:flashMax-1]) + "…"
	}
	return s
}

// setFlash stores one toast for the next page render.
func (a *Admin) setFlash(w http.ResponseWriter, t toast) {
	t.Msg = clip(t.Msg)
	j, _ := json.Marshal(t)
	p := base64.RawURLEncoding.EncodeToString(j)
	http.SetCookie(w, &http.Cookie{
		Name: flashCookie, Value: p + "." + a.sign("flash:"+p), Path: "/admin",
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: a.secure(), MaxAge: int(flashTTL.Seconds()),
	})
}

// takeFlash returns the pending toast, if any, and clears the cookie. A missing, forged or
// malformed cookie yields nothing.
func (a *Admin) takeFlash(w http.ResponseWriter, r *http.Request) (toast, bool) {
	c, err := r.Cookie(flashCookie)
	if err != nil {
		return toast{}, false
	}
	http.SetCookie(w, &http.Cookie{Name: flashCookie, Value: "", Path: "/admin", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: a.secure()})
	p, sig, ok := strings.Cut(c.Value, ".")
	if !ok || subtle.ConstantTimeCompare([]byte(a.sign("flash:"+p)), []byte(sig)) != 1 {
		return toast{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(p)
	var t toast
	if err != nil || json.Unmarshal(raw, &t) != nil || t.Msg == "" || (t.Kind != toastOK && t.Kind != toastBad) {
		return toast{}, false
	}
	return t, true
}

// back redirects (303) to a page and queues a success or error toast for it.
func (a *Admin) back(w http.ResponseWriter, r *http.Request, to, ok, errMsg string) {
	switch {
	case errMsg != "":
		log.Printf("admin: %s %s refused: %s", r.Method, r.URL.Path, clip(errMsg))
		a.setFlash(w, toast{toastBad, errMsg})
	case ok != "":
		a.setFlash(w, toast{toastOK, ok})
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}
