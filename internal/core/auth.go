package core

import (
	"encoding/json"
	"regexp"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

type descriptor struct {
	Type      string `json:"type"`
	Variant   string `json:"variant"`
	AccountID string `json:"account_id,omitempty"`
}

var accountIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,48}$`)

func readDescriptor(raw []byte) (descriptor, error) {
	var d descriptor
	if err := json.Unmarshal(raw, &d); err != nil {
		return d, badRequest("invalid Qoder auth descriptor JSON")
	}
	if d.Type != "qoder" || (d.Variant != "global" && d.Variant != "cn") {
		return d, badRequest("Qoder descriptor requires type=qoder and variant=global or cn")
	}
	if d.AccountID != "" && !accountIDPattern.MatchString(d.AccountID) {
		return d, badRequest("Qoder account_id must contain only letters, digits, hyphens or underscores")
	}
	return d, nil
}
func authData(d descriptor) pluginapi.AuthData {
	id, label := "qoder", "Qoder"
	if d.Variant == "cn" {
		id, label = "qodercn", "QoderCN"
	}
	if d.AccountID != "" {
		id += "-" + d.AccountID
		label += " (" + d.AccountID + ")"
	}
	raw, _ := json.Marshal(d)
	metadata := map[string]any{"type": "qoder", "variant": d.Variant, "anonymous_models": false}
	attributes := map[string]string{"variant": d.Variant}
	if d.AccountID != "" {
		metadata["account_id"] = d.AccountID
		attributes["account_id"] = d.AccountID
	}
	return pluginapi.AuthData{Provider: "qoder", ID: id, FileName: id + ".json", Label: label, StorageJSON: raw, Metadata: metadata, Attributes: attributes, NextRefreshAfter: time.Now().Add(30 * time.Minute)}
}
func (p *Provider) withAuthState(a pluginapi.AuthData, d descriptor) (pluginapi.AuthData, error) {
	loggedIn, err := p.hasLogin(d)
	if err != nil {
		return a, err
	}
	if a.Metadata == nil {
		a.Metadata = map[string]any{}
	}
	a.Metadata["authenticated"] = loggedIn
	a.Metadata["login_command"] = p.loginCommand(d)
	a.Metadata["anonymous_models"] = false
	return a, nil
}

func (p *Provider) ParseAuth(req pluginapi.AuthParseRequest) (pluginapi.AuthParseResponse, error) {
	var probe descriptor
	if err := json.Unmarshal(req.RawJSON, &probe); err != nil || probe.Type != "qoder" {
		return pluginapi.AuthParseResponse{}, nil
	}
	d, err := readDescriptor(req.RawJSON)
	if err != nil {
		return pluginapi.AuthParseResponse{Handled: true}, err
	}
	a := authData(d)
	if req.FileName != "" {
		a.FileName = req.FileName
	}
	return pluginapi.AuthParseResponse{Handled: true, Auth: a}, nil
}
