package main

import (
	"strings"
	"testing"
)

// The check is exact about which declaration a route mounts, and about which it does not.
func TestScopeCheckFindsEveryScopedRouteThatJudgesNothing(t *testing.T) {
	findings, err := RunScopeCheck("testdata/badscope")
	if err != nil {
		t.Fatalf("RunScopeCheck: %v", err)
	}

	// Keyed by the route, because the route is what a reader has to go and fix.
	want := map[string]bool{
		"POST /unsettled": false,
		"POST /closure":   false,
		"POST /free":      false,
	}
	for _, f := range findings {
		route := routeOf(f.Message)
		if _, expected := want[route]; !expected {
			t.Errorf("unexpected finding %s:%d — %s", f.File, f.Line, f.Message)
			continue
		}
		want[route] = true
		if f.Line == 0 {
			t.Errorf("%s: no line number; a finding must be navigable from the CI log", route)
		}
		if !strings.Contains(f.Hint, "rbac.Authorize") {
			t.Errorf("%s: the hint does not say what to do instead: %s", route, f.Hint)
		}
	}
	for route, found := range want {
		if !found {
			t.Errorf("%s defers resource scope to nothing and was not reported", route)
		}
	}
}

// The collision class readpath had twice: `unsettled` is declared on two types in the
// package, and only the one the route does not mount settles. A check that found handlers
// by name would either report the wrong one or, worse, find the settling one and say
// nothing — which is the failure that matters, because this rule reports a *missing* call.
func TestScopeCheckIsNotFooledByTwoDeclarationsOfOneName(t *testing.T) {
	findings, err := RunScopeCheck("testdata/badscope")
	if err != nil {
		t.Fatalf("RunScopeCheck: %v", err)
	}
	for _, f := range findings {
		if routeOf(f.Message) == "POST /unsettled" {
			if !strings.Contains(f.Message, "Handlers.unsettled") {
				t.Errorf("the finding does not name the mounted declaration: %s", f.Message)
			}
			return
		}
	}
	t.Error("POST /unsettled was not reported; a second declaration of the name hid it")
}

// A handler that settles through a field's method is settled. readpath was blind to this
// shape for a while and it cost it the photo route; the same walk is used here.
func TestScopeCheckFollowsAHandlerThroughItsFields(t *testing.T) {
	findings, err := RunScopeCheck("testdata/badscope")
	if err != nil {
		t.Fatalf("RunScopeCheck: %v", err)
	}
	for _, f := range findings {
		if routeOf(f.Message) == "POST /settled-via-field" {
			t.Fatalf("h.obs.judge settles the debt and was reported anyway: %s", f.Message)
		}
	}
}

// routeOf reads the route back out of a finding's message, which begins with it.
func routeOf(message string) string {
	method, rest, ok := strings.Cut(message, " ")
	if !ok {
		return message
	}
	pattern, _, _ := strings.Cut(rest, " ")
	return method + " " + pattern
}
