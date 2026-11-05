//go:build !unix

package app

func lockDataDir(string) (func(), error) { return func() {}, nil }
