package core

import "io"

func Dispatch(method string, raw []byte) ([]byte, error) { return dispatch(method, raw) }

func Shutdown() {
	if p := current.Load(); p != nil {
		p.Close()
	}
}

func ServeMCP(schemaPath string, input io.Reader, output io.Writer) error {
	return serveMCP(schemaPath, input, output)
}
