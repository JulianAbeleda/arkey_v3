package gpu

import (
	"context"
	"testing"
)

func TestMetalDetectionAndDevices(t *testing.T) {
	got, err := detectPlatform(context.Background(), fake{"Apple M3\n", nil})
	if err != nil || got.Vendor != Metal || got.Name != "Apple M3" {
		t.Fatal(got, err)
	}
	for _, tc := range []struct {
		output string
		want   Backend
	}{
		{"Available devices:\n  MTL0: Apple M3 (12124 MiB, 12123 MiB free)", Backend(Metal)},
		{"Available devices:\n  BLAS: Accelerate (0 MiB, 0 MiB free)\n  :  (0 MiB, 0 MiB free)", CPU},
	} {
		got, err := (DeviceInspector{fake{tc.output, nil}}).Backend(context.Background(), "llama-server")
		if err != nil || got != tc.want {
			t.Fatal(got, err)
		}
	}
}
