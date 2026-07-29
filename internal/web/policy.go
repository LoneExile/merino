package web

import "github.com/LoneExile/merino/internal/app"

// Policy decides what an authenticated identity may do with a given pane.
//
// This is deliberately separate from authentication. Logging in proves who you
// are; Policy decides what you may touch. Conflating the two is how a login
// form ends up gating nothing but the front door — every signed-in user able
// to read every pane's output and, later, type into every agent's terminal.
//
// It is also distinct from app.Guard, which answers a different question:
// Guard validates that a pane ID is real and that a payload is well-formed and
// allowlisted. It has no concept of who is asking. Both checks are required —
// Guard for "is this a sane request", Policy for "may this user make it".
type Policy interface {
	// CanView reports whether the identity may see a pane and read its output.
	// Pane output routinely contains source code, credentials and command
	// history, so this is a real boundary, not cosmetic filtering.
	CanView(id Identity, agent app.Agent) bool

	// CanControl reports whether the identity may write to a pane: approvals,
	// keys, interrupts. Unused while the server is read-only, defined now so
	// the write path cannot be added without confronting the question.
	CanControl(id Identity, agent app.Agent) bool

	// CanSpawn reports whether the identity may create a NEW agent pane.
	//
	// Separate from CanControl because it has no target to scope it: a policy
	// that restricts an identity to certain panes cannot express "and may
	// conjure more" through CanControl, which is only ever asked about a pane
	// that already exists. Spawning starts a process on the operator's
	// machine, so it gets its own answer rather than one inferred from a
	// per-pane question.
	CanSpawn(id Identity) bool
}

// SingleOperator grants any authenticated identity full access to every pane.
//
// This matches the current deployment exactly: one person, their own machine,
// their own agents, one password. It is the honest encoding of that
// assumption rather than an accident.
//
// It stops being correct the moment more than one human can log in — which is
// precisely what integrating Keycloak will make possible. At that point
// replace this with a policy that maps realm roles or pane ownership to
// access, rather than widening this one.
type SingleOperator struct{}

func (SingleOperator) CanView(Identity, app.Agent) bool    { return true }
func (SingleOperator) CanControl(Identity, app.Agent) bool { return true }
func (SingleOperator) CanSpawn(Identity) bool              { return true }

// RequireRole grants access only to identities carrying a named role.
//
// Provided as the intended Keycloak-era replacement for SingleOperator: point
// the OIDC provider at realm roles and construct this with, say, "herd-admin".
// Unused today; kept small and tested so the swap is a one-line change in
// main.go rather than a design exercise under pressure.
type RequireRole struct {
	View    string
	Control string
}

func (p RequireRole) CanView(id Identity, _ app.Agent) bool {
	return p.View == "" || hasRole(id, p.View)
}

func (p RequireRole) CanControl(id Identity, _ app.Agent) bool {
	return p.Control != "" && hasRole(id, p.Control)
}

// CanSpawn reuses the Control role rather than adding a third: starting an
// agent and typing into one are the same trust level — both reach a live
// terminal on the operator's machine.
func (p RequireRole) CanSpawn(id Identity) bool {
	return p.Control != "" && hasRole(id, p.Control)
}

func hasRole(id Identity, want string) bool {
	for _, r := range id.Roles {
		if r == want {
			return true
		}
	}
	return false
}

// filterViewable returns only the agents the identity may see.
func filterViewable(p Policy, id Identity, agents []app.Agent) []app.Agent {
	out := make([]app.Agent, 0, len(agents))
	for _, a := range agents {
		if p.CanView(id, a) {
			out = append(out, a)
		}
	}
	return out
}
