package gpu

import "context"

func detectPlatform(context.Context, Runner) (Result, error) {
	return Result{Vendor: Unknown, Name: "No supported GPU"}, nil
}
