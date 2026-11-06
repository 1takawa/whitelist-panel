package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"anypanel2/internal/model"
	"anypanel2/internal/panel"
	"anypanel2/internal/store"
)

func (s *Server) registerAPI(mux *http.ServeMux) {
	a := s.requireAuth

	mux.HandleFunc("GET /api/inbounds", a(s.listInbounds))
	mux.HandleFunc("POST /api/inbounds", a(s.createInbound))
	mux.HandleFunc("POST /api/inbounds/order", a(s.orderInbounds))
	mux.HandleFunc("GET /api/inbounds/{id}", a(s.getInbound))
	mux.HandleFunc("PUT /api/inbounds/{id}", a(s.updateInbound))
	mux.HandleFunc("DELETE /api/inbounds/{id}", a(s.deleteInbound))
	mux.HandleFunc("GET /api/reality/keys", a(s.realityKeys))
	mux.HandleFunc("POST /api/reality/scan", a(s.realityScan))
	mux.HandleFunc("POST /api/inbounds/{id}/reset-traffic", a(s.resetInboundTraffic))
	mux.HandleFunc("POST /api/xray/vlessenc", a(s.genVlessEnc))
	mux.HandleFunc("POST /api/xray/mldsa65", a(s.genMLDSA65))
	mux.HandleFunc("POST /api/xray/ech", a(s.genECH))
	mux.HandleFunc("GET /api/xray/config/{id}", a(s.xrayConfigPreview))
	mux.HandleFunc("GET /api/xray-profile/{id}", a(s.getXrayProfile))

	mux.HandleFunc("GET /api/clients", a(s.listClients))
	mux.HandleFunc("POST /api/clients", a(s.createClient))
	mux.HandleFunc("GET /api/clients/{id}", a(s.getClient))
	mux.HandleFunc("PUT /api/clients/{id}", a(s.updateClient))
	mux.HandleFunc("DELETE /api/clients/{id}", a(s.deleteClient))
	mux.HandleFunc("POST /api/clients/{id}/reset-traffic", a(s.resetTraffic))
	mux.HandleFunc("POST /api/clients/{id}/devices/{hwid}/block", a(s.blockDevice))
	mux.HandleFunc("DELETE /api/clients/{id}/devices/{hwid}", a(s.deleteDevice))
	mux.HandleFunc("GET /api/clients/{id}/links", a(s.clientLinks))

	mux.HandleFunc("GET /api/xray/status", a(s.xrayStatus))
	mux.HandleFunc("POST /api/xray/restart", a(s.xrayRestart))
	mux.HandleFunc("GET /api/xray/logs", a(s.xrayLogs))

	mux.HandleFunc("GET /api/settings", a(s.getSettings))
	mux.HandleFunc("PUT /api/settings", a(s.putSettings))
	mux.HandleFunc("GET /api/sub-preview", s.previewImage)
	mux.HandleFunc("GET /api/settings/tokens", a(s.templateTokens))
	mux.HandleFunc("GET /api/routing-presets", a(s.routingPresets))
	mux.HandleFunc("POST /api/settings/preview", a(s.previewTemplate))
	mux.HandleFunc("POST /api/settings/preview-app", a(s.previewApp))
	mux.HandleFunc("GET /api/settings/page-example", a(s.pageExample))
	mux.HandleFunc("GET /api/settings/page-presets", a(s.pagePresets))
	mux.HandleFunc("GET /api/theme-assets/{preset}/{file}", s.themeAsset)
	mux.HandleFunc("POST /api/settings/page-preview", a(s.pagePreviewCreate))
	// The frame that shows the preview is sandboxed, so the browser does not send the session cookie with its request: the
	// address itself, a random id that lives ten minutes, is what lets it in.
	mux.HandleFunc("GET /api/settings/page-preview/{id}", s.pagePreviewShow)

	mux.HandleFunc("GET /api/dashboard", a(s.dashboard))

	mux.HandleFunc("GET /api/external-nodes", a(s.listExternalNodes))
	mux.HandleFunc("POST /api/external-nodes", a(s.createExternalNode))
	mux.HandleFunc("POST /api/external-nodes/import", a(s.importExternalNodes))
	mux.HandleFunc("PUT /api/external-nodes/{id}", a(s.updateExternalNode))
	mux.HandleFunc("DELETE /api/external-nodes/{id}", a(s.deleteExternalNode))

	// The users file may be fetched with the API token; the full file is checked inside the handler (session only).
	mux.HandleFunc("GET /api/db/export", a(s.exportDB))

	// Access that the API token itself must not be able to manage.
	ss := s.requireSession
	mux.HandleFunc("PUT /api/xray-profile/{id}", ss(s.putXrayProfile))
	mux.HandleFunc("PUT /api/account", ss(s.updateAccount))
	mux.HandleFunc("POST /api/db/import", ss(s.importDB))
	mux.HandleFunc("GET /api/api-token", ss(s.apiTokenInfo))
	mux.HandleFunc("POST /api/api-token", ss(s.apiTokenGenerate))
	mux.HandleFunc("DELETE /api/api-token", ss(s.apiTokenRevoke))
	mux.HandleFunc("GET /api/telegram", ss(s.telegramStatus))
	mux.HandleFunc("PUT /api/telegram", ss(s.telegramSave))
	mux.HandleFunc("POST /api/telegram/test", ss(s.telegramTest))

	// The REG.RU account holds the panel's domain: connecting it and creating DNS records is session-only too.
	mux.HandleFunc("GET /api/regru", ss(s.regruStatus))
	mux.HandleFunc("PUT /api/regru", ss(s.regruSave))
	mux.HandleFunc("DELETE /api/regru", ss(s.regruDisconnect))
	mux.HandleFunc("GET /api/regru/domains", ss(s.regruDomains))
	mux.HandleFunc("POST /api/regru/subdomain", ss(s.regruGenerate))
	mux.HandleFunc("POST /api/regru/cleanup", ss(s.regruCleanup))
	mux.HandleFunc("POST /api/regru/certificate", ss(s.regruCertificate))
	mux.HandleFunc("GET /api/update", ss(s.updateCheck))
	mux.HandleFunc("POST /api/update/apply", ss(s.updateApply))
	mux.HandleFunc("GET /api/update/status", ss(s.updateStatus))

	// Machines the panel controls: managing them is a session-only privilege like the tokens above.
	mux.HandleFunc("GET /api/cascades", a(s.listCascades))
	mux.HandleFunc("POST /api/cascades", ss(s.createCascade))
	mux.HandleFunc("POST /api/cascades/check-domain", ss(s.checkCascadeDomain))
	mux.HandleFunc("POST /api/main-node", ss(s.setupMainNode))
	mux.HandleFunc("POST /api/settings/preview-image", ss(s.uploadPreviewImage))
	mux.HandleFunc("DELETE /api/settings/preview-image", ss(s.deletePreviewImage))
	mux.HandleFunc("PUT /api/cascades/{id}", ss(s.renameCascade))
	mux.HandleFunc("DELETE /api/cascades/{id}", ss(s.deleteCascade))
	mux.HandleFunc("GET /api/servers", a(s.listServers))
	mux.HandleFunc("POST /api/servers", ss(s.createServer))
	mux.HandleFunc("PUT /api/servers/{id}", ss(s.updateServer))
	mux.HandleFunc("DELETE /api/servers/{id}", ss(s.deleteServer))
	mux.HandleFunc("POST /api/servers/{id}/test", ss(s.testServer))
	mux.HandleFunc("POST /api/servers/{id}/restart", ss(s.restartServer))
	mux.HandleFunc("POST /api/servers/{id}/token", ss(s.serverToken))
	mux.HandleFunc("POST /api/servers/ssh-check", ss(s.sshCheck))
	mux.HandleFunc("POST /api/servers/install", ss(s.installNode))
	mux.HandleFunc("POST /api/servers/{id}/upgrade", ss(s.upgradeNode))
	mux.HandleFunc("POST /api/servers/{id}/open-ports", ss(s.openNodePorts))
	mux.HandleFunc("POST /api/servers/{id}/reality", ss(s.setupNodeReality))
	mux.HandleFunc("GET /api/servers/{id}/clients", ss(s.serverClients))
	mux.HandleFunc("GET /api/server-jobs/{id}", ss(s.nodeJob))
}

// ---- helpers ----

// result is the envelope for mutations: the saved item plus an optional warning (saved but not applied to Xray).
type result struct {
	Item    any    `json:"item,omitempty"`
	Warning string `json:"warning,omitempty"`
}

func decode[T any](w http.ResponseWriter, r *http.Request) (T, bool) {
	var v T
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return v, false
	}
	return v, true
}

// finish writes the outcome of a service call.
func (s *Server) finish(w http.ResponseWriter, item any, err error) {
	var ae *panel.ApplyError
	if errors.As(err, &ae) {
		s.log.Error("apply to xray failed", "err", ae.Err)
		writeJSON(w, http.StatusOK, result{Item: item, Warning: ae.Error()})
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result{Item: item})
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	var ie *panel.InvalidError
	switch {
	case errors.As(err, &ie):
		writeError(w, http.StatusBadRequest, ie.Msg)
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, "already exists")
	default:
		s.log.Error("request failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

func (s *Server) respond(w http.ResponseWriter, v any, err error) {
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// ---- inbounds ----

func (s *Server) listInbounds(w http.ResponseWriter, r *http.Request) {
	v, err := s.svc.ListInbounds(r.Context())
	s.respond(w, v, err)
}

// orderInbounds saves the order the connections (and the automatic node) were dragged into.
func (s *Server) orderInbounds(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[struct {
		Order []string `json:"order"`
	}](w, r)
	if !ok {
		return
	}
	s.respond(w, map[string]bool{"ok": true}, s.svc.ReorderInbounds(r.Context(), in.Order))
}

func (s *Server) getInbound(w http.ResponseWriter, r *http.Request) {
	v, err := s.svc.GetInbound(r.Context(), r.PathValue("id"))
	s.respond(w, v, err)
}

// createInbound makes a connection and, unless the request says "addToClients": false, puts it into every existing
// client's subscription.
func (s *Server) createInbound(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	var in model.Inbound
	if err == nil {
		err = json.Unmarshal(body, &in)
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	var probe struct {
		AddToClients *bool `json:"addToClients"`
	}
	_ = json.Unmarshal(body, &probe)
	ctx, cancel := detached(r)
	defer cancel()
	v, err := s.svc.CreateInboundFor(ctx, in, probe.AddToClients == nil || *probe.AddToClients)
	s.finish(w, v, err)
}

func (s *Server) updateInbound(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[model.Inbound](w, r)
	if !ok {
		return
	}
	ctx, cancel := detached(r)
	defer cancel()
	v, err := s.svc.UpdateInbound(ctx, r.PathValue("id"), in)
	s.finish(w, v, err)
}

func (s *Server) deleteInbound(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := detached(r)
	defer cancel()
	s.finish(w, nil, s.svc.DeleteInbound(ctx, r.PathValue("id")))
}

func (s *Server) realityKeys(w http.ResponseWriter, _ *http.Request) {
	priv, pub, sid, err := s.svc.NewRealityKeys()
	s.respond(w, map[string]string{"privateKey": priv, "publicKey": pub, "shortId": sid}, err)
}

// ---- clients ----

func (s *Server) listClients(w http.ResponseWriter, r *http.Request) {
	v, err := s.svc.ListClients(r.Context())
	s.respond(w, v, err)
}

func (s *Server) getClient(w http.ResponseWriter, r *http.Request) {
	v, err := s.svc.GetClient(r.Context(), r.PathValue("id"))
	s.respond(w, v, err)
}

// createClient makes a client. A request that does not mention "flow" gets xtls-rprx-vision, like clients made in the
// panel's form; it only applies where it can (VLESS over TCP), so ws/gRPC connections are not affected. Saying
// "flow":"" explicitly turns it off. A request without "inboundIds" puts the client on every enabled connection, as the
// form and the bot do: a client on none has nothing in its subscription. "inboundIds":[] means none on purpose.
func (s *Server) createClient(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	var c model.Client
	if err == nil {
		err = json.Unmarshal(body, &c)
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	var probe struct {
		Flow       *string   `json:"flow"`
		InboundIDs *[]string `json:"inboundIds"`
	}
	_ = json.Unmarshal(body, &probe)
	if probe.Flow == nil {
		c.Flow = model.FlowVision
	}
	if probe.InboundIDs == nil {
		ins, err := s.svc.ListInbounds(r.Context())
		if err != nil {
			s.fail(w, err)
			return
		}
		for _, in := range ins {
			c.InboundIDs = append(c.InboundIDs, in.ID) // every connection, enabled or not: its switch decides who sees it
		}
	}
	v, err := s.svc.CreateClient(r.Context(), c)
	s.finish(w, v, err)
}

func (s *Server) updateClient(w http.ResponseWriter, r *http.Request) {
	c, ok := decode[model.Client](w, r)
	if !ok {
		return
	}
	v, err := s.svc.UpdateClient(r.Context(), r.PathValue("id"), c)
	s.finish(w, v, err)
}

func (s *Server) deleteClient(w http.ResponseWriter, r *http.Request) {
	s.finish(w, nil, s.svc.DeleteClient(r.Context(), r.PathValue("id")))
}

func (s *Server) resetTraffic(w http.ResponseWriter, r *http.Request) {
	v, err := s.svc.ResetTraffic(r.Context(), r.PathValue("id"))
	s.finish(w, v, err)
}

func (s *Server) clientLinks(w http.ResponseWriter, r *http.Request) {
	v, err := s.svc.ClientLinks(r.Context(), r.PathValue("id"))
	s.respond(w, v, err)
}

// ---- xray ----

func (s *Server) xrayStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.svc.XrayStatus(r.Context()))
}

func (s *Server) xrayRestart(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.XrayRestart(r.Context()); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.svc.XrayStatus(r.Context()))
}

func (s *Server) xrayLogs(w http.ResponseWriter, r *http.Request) {
	n, _ := strconv.Atoi(r.URL.Query().Get("n"))
	if n <= 0 || n > 500 {
		n = 200
	}
	lines := s.svc.XrayLogs(n)
	if lines == nil {
		lines = []string{} // JSON null would crash the page that renders the log
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": lines})
}

// ---- settings ----

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	v, err := s.svc.GetSettings(r.Context())
	s.respond(w, v, err)
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[model.Settings](w, r)
	if !ok {
		return
	}
	v, err := s.svc.PutSettings(r.Context(), in)
	if err == nil {
		s.invalidateSubPath()
	}
	s.respond(w, v, err)
}

func (s *Server) templateTokens(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.svc.TemplateTokens())
}

// previewTemplate renders a remark/metadata template for the settings UI, with a sample or a real client.
func (s *Server) previewTemplate(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[struct {
		Kind     string `json:"kind"`
		Template string `json:"template"`
		ClientID string `json:"clientId"`
		TimeZone string `json:"timeZone"`
	}](w, r)
	if !ok {
		return
	}
	out, err := s.svc.PreviewTemplate(r.Context(), req.Kind, req.Template, req.ClientID, req.TimeZone)
	s.respond(w, map[string]string{"result": out}, err)
}

// ---- external nodes ----

func (s *Server) listExternalNodes(w http.ResponseWriter, r *http.Request) {
	v, err := s.svc.ListExternalNodes(r.Context())
	s.respond(w, v, err)
}

func (s *Server) createExternalNode(w http.ResponseWriter, r *http.Request) {
	n, ok := decode[model.ExternalNode](w, r)
	if !ok {
		return
	}
	v, err := s.svc.CreateExternalNode(r.Context(), n)
	s.respond(w, v, err)
}

func (s *Server) updateExternalNode(w http.ResponseWriter, r *http.Request) {
	n, ok := decode[model.ExternalNode](w, r)
	if !ok {
		return
	}
	v, err := s.svc.UpdateExternalNode(r.Context(), r.PathValue("id"), n)
	s.respond(w, v, err)
}

func (s *Server) deleteExternalNode(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.DeleteExternalNode(r.Context(), r.PathValue("id")); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) importExternalNodes(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[struct {
		Text string `json:"text"`
	}](w, r)
	if !ok {
		return
	}
	v, err := s.svc.ImportExternalNodes(r.Context(), req.Text)
	s.respond(w, v, err)
}

// previewApp renders what a client app would show for the settings in the form (not yet saved), for a sample client.
func (s *Server) previewApp(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[struct {
		Subscription model.SubSettings `json:"subscription"`
		State        string            `json:"state"`
	}](w, r)
	if !ok {
		return
	}
	out, err := s.svc.PreviewApp(r.Context(), req.Subscription, req.State)
	s.respond(w, out, err)
}

// ---- Xray settings, generators ----

func (s *Server) realityScan(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[struct {
		Target     string `json:"target"`
		ServerName string `json:"serverName"`
	}](w, r)
	if !ok {
		return
	}
	v, err := s.svc.ScanTarget(r.Context(), req.Target, req.ServerName)
	s.respond(w, v, err)
}

func (s *Server) resetInboundTraffic(w http.ResponseWriter, r *http.Request) {
	v, err := s.svc.ResetInboundTraffic(r.Context(), r.PathValue("id"))
	s.finish(w, v, err)
}

func (s *Server) genVlessEnc(w http.ResponseWriter, r *http.Request) {
	v, err := s.svc.GenerateVlessEnc(r.Context())
	s.respond(w, v, err)
}

func (s *Server) genMLDSA65(w http.ResponseWriter, r *http.Request) {
	seed, verify, err := s.svc.GenerateMLDSA65(r.Context())
	s.respond(w, map[string]string{"seed": seed, "verify": verify}, err)
}

func (s *Server) genECH(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[struct {
		ServerName string `json:"serverName"`
	}](w, r)
	if !ok {
		return
	}
	keys, list, err := s.svc.GenerateECH(r.Context(), req.ServerName)
	s.respond(w, map[string]string{"echServerKeys": keys, "echConfigList": list}, err)
}

func (s *Server) xrayConfigPreview(w http.ResponseWriter, r *http.Request) {
	b, err := s.svc.PreviewConfig(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
}

func (s *Server) getXrayProfile(w http.ResponseWriter, r *http.Request) {
	v, err := s.svc.GetXrayProfile(r.Context(), r.PathValue("id"))
	s.respond(w, v, err)
}

func (s *Server) putXrayProfile(w http.ResponseWriter, r *http.Request) {
	p, ok := decode[model.XrayProfile](w, r)
	if !ok {
		return
	}
	v, err := s.svc.PutXrayProfile(r.Context(), r.PathValue("id"), p)
	s.finish(w, v, err)
}

// routingPresets returns the RoscomVPN routing profiles for the settings page: ?app=happ or ?app=incy.
func (s *Server) routingPresets(w http.ResponseWriter, r *http.Request) {
	v, err := s.svc.RoutingPresets(r.Context(), r.URL.Query().Get("app"), r.URL.Query().Get("refresh") == "1")
	s.respond(w, v, err)
}
