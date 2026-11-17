package sessions

import "testing"

func TestTransitions(t *testing.T) {
	happy := []State{Created, Inspecting, Planned, AwaitingApproval, Preparing, Building, Starting, Verifying, Ready, Stopping, Stopped, Removing, Removed}
	x := &Session{ID: NewID(), State: Created}
	for _, s := range happy[1:] {
		if err := x.Transition(s); err != nil {
			t.Fatalf("legal transition rejected: %v", err)
		}
	}
	illegal := [][2]State{
		{AwaitingApproval, Ready}, // cannot skip execution
		{Planned, Preparing},      // cannot execute without approval state
		{Building, Ready},         // cannot be ready without verifying
		{Starting, Ready},         // ditto
		{Removed, Ready},          // terminal
		{Created, Building},
	}
	for _, c := range illegal {
		x := &Session{ID: NewID(), State: c[0]}
		if err := x.Transition(c[1]); err == nil {
			t.Errorf("%s -> %s must be illegal", c[0], c[1])
		}
	}
	if !Removed.Terminal() || Ready.Executing() || !Building.Executing() || !Ready.Active() {
		t.Fatal("state predicates wrong")
	}
	// Every state referenced in the table is a known state.
	for from, tos := range transitions {
		for _, to := range tos {
			if _, ok := transitions[to]; !ok {
				t.Errorf("%s -> %s: target has no transition entry", from, to)
			}
		}
	}
}

func TestIDs(t *testing.T) {
	id := NewID()
	if !ValidID(id) {
		t.Fatalf("generated invalid id %q", id)
	}
	for _, bad := range []string{"", "zs", "zs123", "ZS1a2b3c4d", "zs1a2b3c4d; rm", "../zs1a2b3c4d", "zs1a2b3c4g"} {
		if ValidID(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}
