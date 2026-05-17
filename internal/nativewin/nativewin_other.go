//go:build !linux && !darwin

package nativewin

func Available() bool { return false }

func Open(_ Options) error { return ErrUnsupported }
