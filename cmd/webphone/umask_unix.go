//go:build unix

package main

import "syscall"

// Files the server creates (database, key, certificate) are private to its user.
func init() { syscall.Umask(0o077) }
