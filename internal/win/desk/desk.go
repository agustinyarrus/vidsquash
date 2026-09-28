//go:build windows

// Package desk es lo poco de la API de Windows que hace falta en la consola:
// el modo y el tamaño de la consola (tui); el portapapeles queda
// disponible aunque vidsquash no lo use. Es chico a propósito: nada de red ni de
// lanzar procesos, así el .exe no carga con lo que no usa. Todo por
// syscall, sin CGO.
package desk

import (
	"syscall"
	"unicode/utf16"
	"unsafe"
)

// kernel32 es una KnownDLL: Windows la carga siempre desde System32, así que
// se puede nombrar sin ruta. user32 se carga por ruta absoluta (con el nombre
// pelado, LoadLibrary busca primero junto al exe: DLL preloading).
var (
	modkernel32             = syscall.NewLazyDLL("kernel32.dll")
	procGetSystemDirectoryW = modkernel32.NewProc("GetSystemDirectoryW")
	moduser32               = syscall.NewLazyDLL(systemDirectory() + `\user32.dll`)
)

func systemDirectory() string {
	buf := make([]uint16, 512)
	n, _, _ := syscall.SyscallN(procGetSystemDirectoryW.Addr(), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 || int(n) >= len(buf) {
		return `C:\Windows\System32`
	}
	return string(utf16.Decode(buf[:n]))
}

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
