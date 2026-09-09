package gpu

import "context"

func detectPlatform(context.Context, Runner) (Result, error) {
	return Result{Unknown, "No supported GPU", 0}, nil
}
