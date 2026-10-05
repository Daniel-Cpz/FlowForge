package job

import (
	"errors"
	"testing"
)

func TestTransitions(t *testing.T) {
	states := []Status{Queued, Running, Succeeded, Failed, Retrying, DeadLetter, Cancelled, TimedOut, "UNKNOWN", ""}
	allowed := map[[2]Status]bool{
		{Queued, Running}: true, {Queued, Cancelled}: true,
		{Running, Succeeded}: true, {Running, Failed}: true, {Running, TimedOut}: true, {Running, Cancelled}: true,
		{Failed, Retrying}: true, {Failed, DeadLetter}: true, {Retrying, Queued}: true, {Retrying, Cancelled}: true,
		{TimedOut, Retrying}: true, {TimedOut, DeadLetter}: true,
	}
	for _, from := range states {
		for _, to := range states {
			t.Run(string(from)+"_to_"+string(to), func(t *testing.T) {
				want := allowed[[2]Status{from, to}]
				if CanTransition(from, to) != want {
					t.Fatal("incorrect transition permission")
				}
				j := Job{Status: from}
				err := j.Transition(to)
				if want {
					if err != nil || j.Status != to {
						t.Fatal("valid transition rejected", err)
					}
				} else {
					if !errors.Is(err, ErrInvalidTransition) || j.Status != from {
						t.Fatal("invalid transition changed state", err)
					}
				}
			})
		}
	}
}

func TestTerminalStates(t *testing.T) {
	for _, s := range []Status{Succeeded, DeadLetter, Cancelled} {
		if !s.Terminal() {
			t.Errorf("%s must be terminal", s)
		}
	}
	for _, s := range []Status{Queued, Running, Failed, Retrying, TimedOut, "UNKNOWN"} {
		if s.Terminal() {
			t.Errorf("%s must not be terminal", s)
		}
	}
}
