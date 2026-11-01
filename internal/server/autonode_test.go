package server

import (
	"testing"

	"anypanel2/internal/links"
	"anypanel2/internal/model"
)

func TestHappAndIncyGetJSONOnlyWhenTheAutoNodeCanBeBuilt(t *testing.T) {
	two := []links.Endpoint{{Name: "a"}, {Name: "b"}}
	on := model.SubSettings{AutoNode: model.AutoNodeSettings{Enable: true}}

	for _, ua := range []string{"Happ/2.1.0/ios", "INCY/1.4 Android", "happ-desktop"} {
		if !wantsAutoNodeJSON(on, ua, two) {
			t.Errorf("%s must get the JSON format when the automatic node is on", ua)
		}
	}
	if wantsAutoNodeJSON(on, "v2rayN/6.0", two) || wantsAutoNodeJSON(on, "Mozilla/5.0", two) {
		t.Error("other apps keep the plain list")
	}
	if wantsAutoNodeJSON(model.SubSettings{}, "Happ/2.1.0", two) {
		t.Error("nothing changes while the automatic node is off")
	}
	keep := on
	keep.AutoNode.KeepLinks = true
	if wantsAutoNodeJSON(keep, "Happ/2.1.0", two) {
		t.Error("KeepLinks leaves Happ on the plain list")
	}
	if wantsAutoNodeJSON(on, "Happ/2.1.0", two[:1]) {
		t.Error("with a single server there is no group to build")
	}
	excl := []links.Endpoint{{Name: "a"}, {Name: "b", NoAuto: true}}
	if wantsAutoNodeJSON(on, "Happ/2.1.0", excl) {
		t.Error("servers excluded from the automatic node do not count")
	}
}
