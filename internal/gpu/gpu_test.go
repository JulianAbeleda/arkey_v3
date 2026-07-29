package gpu

import (
	"context"
	"testing"
)

type fake struct {
	out string
	err error
}

func (f fake) Run(context.Context, string, ...string) ([]byte, error) { return []byte(f.out), f.err }
func TestBackend(t *testing.T) {
	b, e := LDDInspector{fake{"libggml-cuda.so", nil}}.Backend(context.Background(), "x")
	if e != nil || b != CUDABackend {
		t.Fatal(b, e)
	}
}
