//go:build darwin

package app

import (
	"context"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// Wails discovers this hook by type-asserting the service against
// application.ServiceStartup (application.go:714) — it is never called by
// name from our code. So a signature that drifts by one type would not fail
// to compile, it would simply never run, and the tray would come up empty
// against a live herd. This assertion makes that drift a build error.
var _ application.ServiceStartup = (*AgentsService)(nil)

// ServiceStartup adapts Start to the Wails service lifecycle, which is how
// the menubar app boots this service: main.go hands it to
// application.NewService and Wails calls this method by interface, never by
// name from our code.
//
// This file is the entire Wails surface of package app, and it is
// darwin-only so that internal/web — which imports app on the read path of
// the web dashboard — stays buildable for linux. CI asserts that with a
// go list -deps check; see .github/workflows/ci.yml.
func (s *AgentsService) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	return s.Start(ctx)
}
