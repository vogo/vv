package debugs

import "os"

func getEnv(k string) string { return os.Getenv(k) }
