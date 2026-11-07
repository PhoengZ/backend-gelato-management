package models

import "testing"

func TestCanTransition(t *testing.T) {
	all := []Status{StatusQueued, StatusPreparing, StatusReady, StatusPickedUp, StatusCancelled}
	allowed := map[[2]Status]bool{
		{StatusQueued, StatusPreparing}: true,
		{StatusPreparing, StatusReady}:  true,
		{StatusReady, StatusPickedUp}:   true,
	}
	for _, from := range all {
		for _, to := range all {
			if got, want := CanTransition(from, to), allowed[[2]Status{from, to}]; got != want {
				t.Errorf("CanTransition(%s, %s) = %v, want %v", from, to, got, want)
			}
		}
	}
}

func TestIsValid(t *testing.T) {
	for s, want := range map[Status]bool{
		StatusQueued: true, StatusPreparing: true, StatusReady: true, StatusPickedUp: true, StatusCancelled: true, "BOGUS": false, "": false,
	} {
		if got := s.IsValid(); got != want {
			t.Errorf("%q.IsValid() = %v, want %v", s, got, want)
		}
	}
}

func TestIsUpdatable(t *testing.T) {
	for s, want := range map[Status]bool{
		StatusQueued: false, StatusPreparing: true, StatusReady: true, StatusPickedUp: true, StatusCancelled: false, "BOGUS": false,
	} {
		if got := s.IsUpdatable(); got != want {
			t.Errorf("%s.IsUpdatable() = %v, want %v", s, got, want)
		}
	}
}

func TestIsCancellable(t *testing.T) {
	for s, want := range map[Status]bool{
		StatusQueued: true, StatusPreparing: true, StatusReady: false, StatusPickedUp: false, StatusCancelled: false,
	} {
		if got := s.IsCancellable(); got != want {
			t.Errorf("%s.IsCancellable() = %v, want %v", s, got, want)
		}
	}
}
