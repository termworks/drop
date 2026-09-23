//go:build tools

package mobile

// gomobile generates a binding that imports this, so the module has to require it. Nothing here
// calls it, and without the import `go mod tidy` drops the requirement and the bind fails.
import _ "golang.org/x/mobile/bind"
