package openapi

import (
	"reflect"
	"testing"
)

func TestDiffOps(t *testing.T) {
	a := []Op{{Key: "GET /a", Summary: "one"}, {Key: "GET /b"}, {Key: "GET /c", Summary: "same"}}
	b := []Op{{Key: "GET /a", Summary: "uno"}, {Key: "GET /c", Summary: "same"}, {Key: "POST /d"}}
	got := DiffOps(a, b)
	want := OpDiff{Added: []string{"POST /d"}, Removed: []string{"GET /b"}, Changed: []string{"GET /a"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
	if !DiffOps(a, a).Empty() {
		t.Error("the same operations are no difference")
	}
	if Hash([]byte("x")) == Hash([]byte("y")) || len(Hash([]byte("x"))) != 64 {
		t.Error("hash")
	}
}
