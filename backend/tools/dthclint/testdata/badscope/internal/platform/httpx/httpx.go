package httpx

// Stand-in for the real platform package: the check reads the *shape* of a mount call, so
// these need only be nameable.

type Requirement struct{}

func Permission(anyOf ...string) Requirement       { return Requirement{} }
func PermissionScoped(anyOf ...string) Requirement { return Requirement{} }
func Declare(r Requirement, h any) any             { return nil }
