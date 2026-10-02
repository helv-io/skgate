package app

import (
	"github.com/helv-io/skgate/internal/config"
	"strings"
	"testing"
)

func TestAdminHeaderVersion(t *testing.T) {
	_, _, br, _ := signedIn(t, nil)
	_, page := br.get("/admin")
	if !strings.Contains(page, `<span class="ver">v`+config.Version+`</span>`) {
		t.Fatal("version must be in the top bar")
	}
}
