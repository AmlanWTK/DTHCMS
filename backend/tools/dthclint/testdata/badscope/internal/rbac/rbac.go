package rbac

import "context"

// The two service-layer doors the check looks for.

func Authorize(ctx context.Context, action string, resource any) error { return nil }
func AuthorizeCreation(ctx context.Context, action string) error       { return nil }
