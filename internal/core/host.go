package core

import (
	"encoding/json"
	"fmt"
)

type HostCall func(string, any) (json.RawMessage, error)

var callHost HostCall = func(string, any) (json.RawMessage, error) {
	return nil, fmt.Errorf("CPA host callback unavailable")
}

func SetHostCall(call HostCall) {
	if call != nil {
		callHost = call
	}
}
