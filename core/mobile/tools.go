//go:build tools

package mobile

// gomobile bind needs golang.org/x/mobile/bind in go.mod; this import keeps
// `go mod tidy` from dropping it. The tools tag keeps it out of real builds.
import _ "golang.org/x/mobile/bind"
