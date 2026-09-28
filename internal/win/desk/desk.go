//go:build windows

// Package desk es lo poco de la API de Windows que necesita tui para pintar:
// el modo y el tamaño de la consola propia. Es chico a propósito: nada de red
// ni de lanzar procesos, así el .exe de vidsquash no carga con lo que no usa. Todo
// por syscall, sin CGO.
package desk

import "syscall"

// kernel32 es una KnownDLL: Windows la carga siempre desde System32, así que
// se puede nombrar sin ruta (con cualquier otra DLL, LoadLibrary buscaría
// primero junto al exe: DLL preloading).
var modkernel32 = syscall.NewLazyDLL("kernel32.dll")

// call invoca un procedimiento y devuelve el resultado crudo más el errno.
// La directiva uintptrescapes lleva al heap los búferes que se pasan como
// uintptr: sin ella, un búfer chico puede quedar en la pila de la goroutine,
// y si la pila crece entre la conversión y la llamada, Windows escribe en la
// pila vieja y el resultado vuelve vacío.
//
//go:uintptrescapes
func call(p *syscall.LazyProc, args ...uintptr) (uintptr, syscall.Errno) {
	r1, _, e := syscall.SyscallN(p.Addr(), args...)
	return r1, e
}

// lastError convierte el errno en error, nunca nil aunque la API haya fallado
// sin fijar GetLastError (pasa con varias de consola).
func lastError(e syscall.Errno) error {
	if e == 0 {
		return syscall.EINVAL
	}
	return e
}
