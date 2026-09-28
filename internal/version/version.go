// Package version guarda la versión de vidsquash. build.ps1 la pisa con -ldflags
// (-X) para estampar el commit exacto en cada exe.
package version

// Version sigue semver; Commit es el hash corto del árbol que se compiló.
var (
	Version = "0.1.0"
	Commit  = "dev"
)

// String devuelve "0.1.0" o "0.1.0+abc1234" cuando hay commit estampado.
func String() string {
	if Commit == "" || Commit == "dev" {
		return Version
	}
	return Version + "+" + Commit
}
