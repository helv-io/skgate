package config

import "testing"

func TestUpdateCheckCanBeSwitchedOff(t *testing.T) {
	t.Setenv("UPDATE_CHECK", "")
	if Load().UpdateCheckURL != ReleasesURL {
		t.Error("the check is on by default")
	}
	for _, v := range []string{"false", "0", "off", "no"} {
		t.Setenv("UPDATE_CHECK", v)
		if Load().UpdateCheckURL != "" {
			t.Errorf("UPDATE_CHECK=%s must disable the check", v)
		}
	}
}
