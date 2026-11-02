//go:build !darwin

package sysproxy

import "context"

// Changes is nil here: the settings aren't watched, so each check reads them again.
func Changes(context.Context) <-chan struct{} { return nil }
