package handler

import led "example.com/backend/internal/ledger"

// The aliased import must still resolve to the declaring package.
func Write() led.Actor { return led.ActorForTest("physician") }

// The real way in is never reported.
func Proper() led.Actor { return led.FromRequest("physician") }
