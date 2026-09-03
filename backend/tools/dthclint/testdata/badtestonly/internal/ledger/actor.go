package ledger

// Actor cannot be built outside this package: its fields are unexported.
type Actor struct{ who string }

// ActorForTest is the door tests use.
//
//dthclint:testonly
func ActorForTest(who string) Actor { return Actor{who: who} }

// FromRequest is the real way in.
func FromRequest(who string) Actor { return Actor{who: who} }
