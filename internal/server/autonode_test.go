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

func TestRoutingProfileGoesIntoJSONOnlyForTheAppItIsFor(t *testing.T) {
	set := model.SubSettings{
		Happ: model.HappSettings{EnableRouting: true, RoutingRules: "happ://routing/onadd/H"},
		Incy: model.IncySettings{EnableRouting: true, RoutingRules: "incy://routing/onadd/I"},
	}
	for ua, want := range map[string]string{"Happ/2.1.0": "happ://routing/onadd/H", "INCY/1.0": "incy://routing/onadd/I", "v2rayN/6.0": "", "Mozilla/5.0": ""} {
		if got := appRoutingProfile(set, ua); got != want {
			t.Fatalf("%s: %q", ua, got)
		}
	}
	set.Happ.EnableRouting = false
	if got := appRoutingProfile(set, "Happ/2.1.0"); got != "" {
		t.Fatalf("routing switched off: %q", got)
	}
}

func TestHappGetsJSONForAnAutoNodeGroupWithTwoServers(t *testing.T) {
	eps := []links.Endpoint{{ID: "a"}, {ID: "b"}, {ID: "c", NoAuto: true}}
	set := model.SubSettings{AutoNode: model.AutoNodeSettings{Groups: []model.AutoGroup{{ID: "g", Enable: true, Members: []string{"a", "c"}}}}}
	if !wantsAutoNodeJSON(set, "Happ/2.1.0", eps) {
		t.Error("a group of two servers the subscriber has needs the group format, whatever the main node does")
	}
	set.AutoNode.Groups[0].Members = []string{"a", "zzz"}
	if wantsAutoNodeJSON(set, "Happ/2.1.0", eps) {
		t.Error("only one member is there: no group")
	}
	set.AutoNode.Groups[0].Members = []string{"a", "b"}
	set.AutoNode.Groups[0].Enable = false
	if wantsAutoNodeJSON(set, "Happ/2.1.0", eps) {
		t.Error("a group that is off changes nothing")
	}
}
