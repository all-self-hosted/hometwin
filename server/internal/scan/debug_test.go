package scan

import "os"

// SCAN_DEBUG=<dir> writes pictures of the intermediate steps there.
func debugDir() string { return os.Getenv("SCAN_DEBUG") }
