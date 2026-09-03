package handler

// A same-named function from somewhere else is not the marked one.
func ActorForTest(string) string { return "" }

func Unrelated() string { return ActorForTest("x") }
