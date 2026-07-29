package cli

import (
	"errors"
	"reflect"
	"testing"
)

func TestParseCompatibility(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want Options
	}{
		{name: "boot", args: []string{"--boot"}, want: Options{ForceBoot: true}},
		{name: "direct", args: []string{"--no-boot", "exec", "test"}, want: Options{SuppressBoot: true, CodexArgs: []string{"exec", "test"}}},
		{name: "payload flags preserved", args: []string{"exec", "--", "--boot", "--no-boot"}, want: Options{CodexArgs: []string{"exec", "--", "--boot", "--no-boot"}}},
		{name: "model short", args: []string{"-m", "custom"}, want: Options{CodexArgs: []string{"-m", "custom"}, HasModel: true, ModelOverride: "custom"}},
		{name: "config overrides", args: []string{"-c", "model=custom", "--config=model_provider=other"}, want: Options{CodexArgs: []string{"-c", "model=custom", "--config=model_provider=other"}, HasModel: true, HasModelProvider: true, ModelOverride: "custom"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(tc.args)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestParseUsageErrors(t *testing.T) {
	for _, args := range [][]string{{"--boot", "--no-boot"}, {"--boot", "exec", "test"}} {
		if _, err := Parse(args); !errors.Is(err, ErrUsage) {
			t.Fatalf("Parse(%q) error = %v", args, err)
		}
	}
}
