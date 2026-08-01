package web

import "net/http"

// healthzHerd mirrors app.Conn but deliberately omits Socket: it is a
// filesystem path (on this operator's machine, something like
// /Users/alice/.config/herdr/herdr.sock) and this endpoint is unauthenticated
// — reachable by anything that can route to the port, including a public
// tunnel. The path leaks the local OS username and directory layout for no
// operational benefit a probe needs; connected/version/protocol/error are
// all a Kubernetes liveness check or a human running `curl` actually wants.
type healthzHerd struct {
	Connected bool   `json:"connected"`
	Version   string `json:"version"`
	Protocol  int    `json:"protocol"`
	Error     string `json:"error,omitempty"`
}

type healthzResponse struct {
	// Status is "ok" when the herd is reachable, "degraded" otherwise. It is
	// never used to pick the HTTP status code — see handleHealthz.
	Status string      `json:"status"`
	Herd   healthzHerd `json:"herd"`
	Agents int         `json:"agents"`
}

// handleHealthz reports whether this process is alive and, separately,
// whether it can currently see the herd. Unauthenticated and cheap: no page
// render, no insecure-transport check, no session cookie — everything /login
// does to double as today's only public probe target.
//
// The HTTP status is unconditionally 200, even when the herd is
// unreachable. This process IS healthy and serving traffic; a disconnected
// herd is a *reported* condition (see the "status" field), not evidence the
// server itself is broken. Answering 5xx here would hand a Kubernetes
// liveness probe a reason to restart this pod every time herdr — a
// completely separate process — is down, turning one outage into two. A
// caller that cares about herd reachability reads "status" in the body.
func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request, _ Identity) {
	conn := s.src.Connection()

	status := "ok"
	if !conn.Connected {
		status = "degraded"
	}

	writeJSON(w, http.StatusOK, healthzResponse{
		Status: status,
		Herd: healthzHerd{
			Connected: conn.Connected,
			Version:   conn.Version,
			Protocol:  conn.Protocol,
			Error:     conn.Error,
		},
		Agents: s.src.Counts().Total,
	})
}
