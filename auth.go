package main

import (
	"encoding/json"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

type descriptor struct {
	Type    string `json:"type"`
	Variant string `json:"variant"`
}

func readDescriptor(raw []byte) (descriptor, error) {
	var d descriptor
	if err := json.Unmarshal(raw, &d); err != nil {
		return d, badRequest("invalid Qoder auth descriptor JSON")
	}
	if d.Type != "qoder" || (d.Variant != "global" && d.Variant != "cn") {
		return d, badRequest("Qoder descriptor requires type=qoder and variant=global or cn")
	}
	return d, nil
}
func authData(d descriptor) pluginapi.AuthData {
	raw, _ := json.Marshal(d)
	return pluginapi.AuthData{Provider: "qoder", ID: "qoder-" + d.Variant, FileName: "qoder-" + d.Variant + ".json", Label: "Qoder " + d.Variant, StorageJSON: raw, Metadata: map[string]any{"type": "qoder", "variant": d.Variant}, Attributes: map[string]string{"variant": d.Variant}, NextRefreshAfter: time.Now().Add(30 * time.Minute)}
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
