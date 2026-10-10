package models

import "testing"

func TestCanTransition(t *testing.T) {
	all := []Status{StatusPreparing, StatusReady, StatusPickedUp, StatusCancelled}
	allowed := map[[2]Status]bool{
		{StatusPreparing, StatusReady}: true,
		{StatusReady, StatusPickedUp}:  true,
	}
	for _, from := range all {
		for _, to := range all {
			if got, want := CanTransition(from, to), allowed[[2]Status{from, to}]; got != want {
				t.Errorf("CanTransition(%s, %s) = %v, want %v", from, to, got, want)
			}
		}
	}
}

func TestIsUpdatable(t *testing.T) {
	for s, want := range map[Status]bool{
		StatusPreparing: false, StatusReady: true, StatusPickedUp: true, StatusCancelled: false, "BOGUS": false,
	} {
		if got := s.IsUpdatable(); got != want {
			t.Errorf("%s.IsUpdatable() = %v, want %v", s, got, want)
		}
	}
}
