package main

import (
	"os"
	"strings"
)

func emptyWorkDir() (string, func(), error) {
	dir, err := os.MkdirTemp("", "cpa-qoder-")
	return dir, func() { _ = os.RemoveAll(dir) }, err
}
func childEnvironment(_ string, proxy string) []string {
	out := []string{}
	for _, v := range os.Environ() {
		key, _, _ := strings.Cut(v, "=")
		key = strings.ToUpper(key)
		if strings.HasPrefix(key, "QODER") || strings.HasPrefix(key, "ANTHROPIC_") || strings.HasPrefix(key, "OPENAI_") || key == "NODE_OPTIONS" || key == "NODE_TLS_REJECT_UNAUTHORIZED" || key == "BUN_OPTIONS" {
			continue
		}
		out = append(out, v)
	}
	if proxy != "" {
		filtered := out[:0]
		for _, v := range out {
			key, _, _ := strings.Cut(v, "=")
			switch strings.ToUpper(key) {
			case "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY":
				continue
			}
			filtered = append(filtered, v)
		}
		out = append(filtered, "HTTP_PROXY="+proxy, "HTTPS_PROXY="+proxy, "ALL_PROXY="+proxy)
	}
	return out
}
