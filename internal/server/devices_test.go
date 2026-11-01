package server

import (
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
)

func TestDevicesAreRecordedBlockedAndForgotten(t *testing.T) {
	h := newHandler(t)
	subID, _ := subFixture(t, h, "")
	c := loggedIn(t, h)
	app := map[string]string{"Accept": "*/*", "X-Hwid": "abc123DEF", "X-Device-Os": "Android", "X-Ver-Os": "14", "X-Device-Model": "Pixel 8", "User-Agent": "Happ/2.0"}

	// An app without a device id is not a device.
	do(h, http.MethodGet, "/sub/"+subID, "", nil, map[string]string{"Accept": "*/*"})
	var list []struct {
		ID      string `json:"id"`
		Devices []struct {
			HWID    string `json:"hwid"`
			OS      string `json:"os"`
			Model   string `json:"model"`
			Blocked bool   `json:"blocked"`
		} `json:"devices"`
	}
	decodeBody(t, do(h, http.MethodGet, "/api/clients", "", c, nil), &list)
	if len(list) != 1 || len(list[0].Devices) != 0 {
		t.Fatalf("no HWID, no device: %+v", list)
	}
	id := list[0].ID

	if rec := do(h, http.MethodGet, "/sub/"+subID, "", nil, app); rec.Code != 200 {
		t.Fatalf("%d", rec.Code)
	}
	do(h, http.MethodGet, "/sub/"+subID, "", nil, app) // the same device again: still one
	decodeBody(t, do(h, http.MethodGet, "/api/clients", "", c, nil), &list)
	if len(list[0].Devices) != 1 || list[0].Devices[0].HWID != "abc123DEF" || list[0].Devices[0].OS != "Android" || list[0].Devices[0].Model != "Pixel 8" {
		t.Fatalf("the device is remembered once: %+v", list[0].Devices)
	}

	// Blocked: the same request now gets one entry that says so and no servers.
	if rec := do(h, http.MethodPost, "/api/clients/"+id+"/devices/abc123DEF/block", `{"blocked":true}`, c, nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"blocked":true`) {
		t.Fatalf("block: %d %s", rec.Code, rec.Body)
	}
	raw, err := base64.StdEncoding.DecodeString(do(h, http.MethodGet, "/sub/"+subID, "", nil, app).Body.String())
	if err != nil || !strings.HasPrefix(string(raw), "socks://127.0.0.1:1080#") || strings.Contains(string(raw), "vless://") || strings.Contains(string(raw), "\n") {
		t.Fatalf("a blocked device sees no servers: %q %v", raw, err)
	}
	// Another device of the same client is not affected.
	other := map[string]string{"Accept": "*/*", "X-Hwid": "other-device-1"}
	raw, _ = base64.StdEncoding.DecodeString(do(h, http.MethodGet, "/sub/"+subID, "", nil, other).Body.String())
	if !strings.Contains(string(raw), "vless://") {
		t.Fatalf("other devices keep their servers: %q", raw)
	}

	// Editing the client in the form does not wipe the devices.
	if rec := do(h, http.MethodPut, "/api/clients/"+id, `{"email":"alice","enable":true,"totalBytes":5000,"inboundIds":[]}`, c, nil); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	decodeBody(t, do(h, http.MethodGet, "/api/clients", "", c, nil), &list)
	if len(list[0].Devices) != 2 {
		t.Fatalf("devices survive a client edit: %+v", list[0].Devices)
	}

	if rec := do(h, http.MethodPost, "/api/clients/"+id+"/devices/abc123DEF/block", `{"blocked":false}`, c, nil); rec.Code != 200 {
		t.Fatalf("unblock: %d", rec.Code)
	}
	if rec := do(h, http.MethodDelete, "/api/clients/"+id+"/devices/abc123DEF", "", c, nil); rec.Code != 200 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodDelete, "/api/clients/"+id+"/devices/abc123DEF", "", c, nil); rec.Code != 404 {
		t.Fatalf("a forgotten device: %d", rec.Code)
	}
	if rec := do(h, http.MethodDelete, "/api/clients/"+id+"/devices/abc123DEF", "", nil, nil); rec.Code != 401 {
		t.Fatalf("login required: %d", rec.Code)
	}
}
