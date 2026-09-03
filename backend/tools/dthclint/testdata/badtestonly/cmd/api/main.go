package main

import "example.com/backend/internal/ledger"

// A composition root is exempt from the architecture rules and not from this one.
func main() { _ = ledger.ActorForTest("admin") }
