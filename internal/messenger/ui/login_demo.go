// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"time"

	"komarugram/internal/messenger/install"
	"komarugram/internal/messenger/login"
)

// demoInstallTime is how long the demo's installation takes.
const demoInstallTime = 1200 * time.Millisecond

// RequireDemoLogin is RequireLogin for the demo's sign-in, which goes through
// the offer to install as well: the installation there takes its time and
// installs nothing.
func (a *App) RequireDemoLogin(l *login.Login) {
	a.RequireLogin(l)
	a.signIn.install.installFn = demoInstall
}

// demoInstall is install.Install that leaves the system as it is. It has
// no program to start in place of this one, and says so with an empty path.
func demoInstall(ctx context.Context, _ install.Options) (string, error) {
	select {
	case <-time.After(demoInstallTime):
		return "", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}
