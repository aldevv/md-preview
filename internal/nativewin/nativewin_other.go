//go:build !linux && !darwin

package nativewin

// Available reports whether a native window is supported. Always false on
// platforms without a backend (windows, freebsd, etc.).
func Available() bool { return false }

// Open returns ErrUnsupported on platforms without a backend.
func Open(_ Options) error { return ErrUnsupported }
