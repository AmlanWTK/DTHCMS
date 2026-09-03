package ledger

// A call from inside the declaring package, still not a test file.
func Convenience() Actor { return ActorForTest("nurse") }
